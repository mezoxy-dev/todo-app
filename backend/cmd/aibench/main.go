// Command aibench measures how well the configured Gemini model puts todos in
// their categories (step 3 spec 6.6).
//
// It sends the labelled todos of internal/ai/testdata/benchmark.json to the
// model one after the other, with at least AI_MIN_INTERVAL between two calls, and
// prints for each of them what was expected, what the model chose and why; then
// the accuracy against the target of 85%, the todos that were mistaken, a table of
// which category was taken for which, and the median time of a call. The
// categories and their descriptions are those of the database, read from a
// temporary one that is migrated like the real one.
//
// It needs GEMINI_API_KEY and uses the quota of that key; go test does not run
// it. It reads the other settings (AI_MODEL, AI_TIMEOUT, AI_MIN_INTERVAL,
// GEMINI_BASE_URL) like the server does. The key is never printed. Run it from the
// backend directory:
//
//	go run ./cmd/aibench [-set file]
//
// A setting that is wrong would be wrong for every todo, and a quota that is used
// up stays so: the run stops at the first such answer and reports what it has,
// and exits with an error. Any other failure counts as a miss for that todo.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"todoapp/internal/ai"
	"todoapp/internal/db"
)

const (
	defaultSet     = "internal/ai/testdata/benchmark.json"
	targetAccuracy = 85 // percent (step 3 spec 6.6)
	errorColumn    = "hata"
)

// sample is one labelled todo of the set.
type sample struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Expected    string `json:"expected"` // the key of the category it belongs in
}

// outcome is what the model made of a sample.
type outcome struct {
	sample
	got      string        // the key the model chose; empty after an error
	reason   string        // why, in the words of the model
	duration time.Duration // how long the call took; zero after an error
	err      error
}

func (o outcome) hit() bool { return o.err == nil && o.got == o.Expected }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "aibench:", err)
		os.Exit(1)
	}
}

// run measures the model and writes the report to out. It returns an error if
// the measurement could not be made or not be finished; what was measured by
// then is in the report all the same.
func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	flags := flag.NewFlagSet("aibench", flag.ContinueOnError)
	setPath := flags.String("set", defaultSet, "the JSON file with the labelled todos")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	settings, err := ai.SettingsFromEnv(getenv)
	if err != nil {
		return err
	}
	if settings.APIKey == "" {
		return errors.New("GEMINI_API_KEY tanımlı değil; ölçüm için bir Gemini anahtarı gerekir")
	}
	categories, err := offeredCategories(ctx)
	if err != nil {
		return err
	}
	samples, err := readSamples(*setPath)
	if err != nil {
		return err
	}
	if err := validateSamples(samples, categories); err != nil {
		return fmt.Errorf("%s: %w", *setPath, err)
	}

	gemini := ai.NewGemini(ai.GeminiConfig{
		APIKey:  settings.APIKey,
		Model:   settings.Model,
		BaseURL: settings.BaseURL,
		Timeout: settings.Timeout,
	})
	fmt.Fprintf(out, "Model: %s — %d örnek, çağrılar arası en az %s\n\n", settings.Model, len(samples), settings.MinInterval)
	outcomes, stopped := measure(ctx, gemini, categories, samples, settings.MinInterval, func(i int, o outcome) {
		printOutcome(out, i, len(samples), o)
	})
	printSummary(out, categories, outcomes)
	if stopped != nil {
		fmt.Fprintf(out, "\nölçüm tamamlanamadı: %d örnekten %d tanesi ölçüldü (%s)\n", len(samples), len(outcomes), describeError(stopped))
		return fmt.Errorf("ölçüm tamamlanamadı: %w", stopped)
	}
	return nil
}

// offeredCategories returns the categories the model is offered: those of a new
// database, migrated like the real one, without the inbox.
func offeredCategories(ctx context.Context) ([]ai.Category, error) {
	dir, err := os.MkdirTemp("", "aibench-")
	if err != nil {
		return nil, fmt.Errorf("create a temporary directory: %w", err)
	}
	defer os.RemoveAll(dir) // after the Close below: Windows cannot delete an open file
	store, err := db.Open(filepath.Join(dir, "categories.db"))
	if err != nil {
		return nil, fmt.Errorf("open a temporary database: %w", err)
	}
	defer store.Close()

	all, err := store.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	var offered []ai.Category
	for _, c := range all {
		if c.Key != db.InboxKey {
			offered = append(offered, ai.Category{Key: c.Key, Name: c.Name, Description: c.Description})
		}
	}
	return offered, nil
}

// readSamples reads the set from a JSON file: a list of {"title", "description"?,
// "expected"}.
func readSamples(path string) ([]sample, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ölçüm seti okunamadı: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var samples []sample
	if err := dec.Decode(&samples); err != nil {
		return nil, fmt.Errorf("ölçüm seti çözülemedi (%s): %w", path, err)
	}
	return samples, nil
}

// validateSamples checks that the set is not empty and that every sample has a
// title and a category the model can choose.
func validateSamples(samples []sample, categories []ai.Category) error {
	if len(samples) == 0 {
		return errors.New("ölçüm seti boş")
	}
	keys := make([]string, len(categories))
	for i, c := range categories {
		keys[i] = c.Key
	}
	for i, s := range samples {
		if strings.TrimSpace(s.Title) == "" {
			return fmt.Errorf("örnek %d: başlık boş", i+1)
		}
		if !slices.Contains(keys, s.Expected) {
			return fmt.Errorf("örnek %d (%q): beklenen %q kategorilerden biri değil (%s)", i+1, s.Title, s.Expected, strings.Join(keys, ", "))
		}
	}
	return nil
}

// measure sends the samples to the classifier one at a time, at least interval
// from the start of one call to the start of the next, and calls each with every
// outcome. It stops at an answer that will come again for every sample (see
// fatal) or when ctx is done; the outcomes before it are returned, together with
// the reason it stopped. A sample that only failed once, by a timeout or a bad
// answer, is an outcome with an error and the run goes on.
func measure(ctx context.Context, classifier ai.Classifier, categories []ai.Category, samples []sample,
	interval time.Duration, each func(i int, o outcome)) (outcomes []outcome, stopped error) {
	var last time.Time
	for i, s := range samples {
		if i > 0 && interval > 0 {
			timer := time.NewTimer(interval - time.Since(last))
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return outcomes, ctx.Err()
			}
		}
		last = time.Now()
		result, err := classifier.Classify(ctx, ai.Input{Title: s.Title, Description: s.Description, Categories: categories})
		if ctx.Err() != nil {
			return outcomes, ctx.Err()
		}
		o := outcome{sample: s, got: result.Key, reason: result.Reason, duration: result.Duration, err: err}
		each(i, o)
		if fatal(err) {
			return outcomes, err
		}
		outcomes = append(outcomes, o)
	}
	return outcomes, nil
}

// fatal reports whether err will come again for every sample: the quota is used
// up, or the key, the model or the request is refused.
func fatal(err error) bool {
	var quota *ai.QuotaError
	if errors.As(err, &quota) {
		return true
	}
	var httpErr *ai.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.Status {
		case 400, 401, 403, 404:
			return true
		}
	}
	return false
}

// errorBodyLimit is how many bytes of the body of a refusal the report shows.
const errorBodyLimit = 600

// describeError returns err on one line. For an answer of the service that was
// not a success it adds the start of the body, which says what the service
// refused: a field it does not know, a key that is not valid, the quota that ran
// out. (The body has the API key blanked already.)
func describeError(err error) string {
	var body string
	var httpErr *ai.HTTPError
	var quota *ai.QuotaError
	switch {
	case errors.As(err, &httpErr):
		body = httpErr.Body
	case errors.As(err, &quota):
		body = quota.Body
	}
	if body = oneLine(body, errorBodyLimit); body != "" {
		return err.Error() + ": " + body
	}
	return err.Error()
}

// oneLine puts s on one line and cuts it after limit bytes (not in the middle of
// a character), with "…" to say so.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// printOutcome writes the line of one sample: its place in the set, whether the
// model was right, the title, and what the model said.
func printOutcome(out io.Writer, i, total int, o outcome) {
	label, detail := "OK", ""
	switch {
	case o.err != nil:
		label, detail = "HATA", describeError(o.err)
	case o.hit():
		detail = fmt.Sprintf("%s (%s sn)  %s", o.got, seconds(o.duration), o.reason)
	default:
		label = "YANLIŞ"
		detail = fmt.Sprintf("beklenen: %s, bulunan: %s (%s sn)  %s", o.Expected, o.got, seconds(o.duration), o.reason)
	}
	fmt.Fprintf(out, "[%*d/%d] %-6s %-44.44s  %s\n", len(strconv.Itoa(total)), i+1, total, label, o.Title, detail)
}

// printSummary writes the accuracy, the mistakes, the accuracy per category, the
// table of what was taken for what, and the median time.
func printSummary(out io.Writer, categories []ai.Category, outcomes []outcome) {
	fmt.Fprintln(out)
	total := len(outcomes)
	if total == 0 {
		fmt.Fprintln(out, "İsabet: ölçülen örnek yok")
		return
	}

	var hits, errs int
	var durations []time.Duration
	for _, o := range outcomes {
		switch {
		case o.hit():
			hits++
		case o.err != nil:
			errs++
		}
		if o.err == nil {
			durations = append(durations, o.duration)
		}
	}
	verdict := "ALTINDA"
	if float64(hits)*100/float64(total) >= targetAccuracy {
		verdict = "ULAŞILDI"
	}
	fmt.Fprintf(out, "İsabet: %d/%d (%%%s) — hedef %%%d: %s\n", hits, total, percent(hits, total), targetAccuracy, verdict)
	fmt.Fprintf(out, "Hata (yanıt alınamayan): %d\n", errs)
	if len(durations) > 0 {
		fmt.Fprintf(out, "Medyan süre: %s sn (%d çağrı)\n", seconds(median(durations)), len(durations))
	} else {
		fmt.Fprintln(out, "Medyan süre: -")
	}

	// The todos the model got wrong, with its reason: what to improve in the
	// descriptions of the categories.
	var wrong []outcome
	for _, o := range outcomes {
		if o.err == nil && !o.hit() {
			wrong = append(wrong, o)
		}
	}
	if len(wrong) > 0 {
		fmt.Fprintln(out, "\nYanlış sınıflananlar:")
		for _, o := range wrong {
			fmt.Fprintf(out, "  - %s (beklenen: %s, bulunan: %s): %s\n", o.Title, o.Expected, o.got, o.reason)
		}
	}

	// Per category, and what each was taken for. The rows are the categories the
	// set has todos for; the columns are all the categories, then the failures.
	counts := map[string]map[string]int{} // expected -> found (or errorColumn) -> number
	perCategory := map[string][2]int{}    // expected -> hits, total
	for _, o := range outcomes {
		found := o.got
		if o.err != nil {
			found = errorColumn
		}
		if counts[o.Expected] == nil {
			counts[o.Expected] = map[string]int{}
		}
		counts[o.Expected][found]++
		p := perCategory[o.Expected]
		if o.hit() {
			p[0]++
		}
		p[1]++
		perCategory[o.Expected] = p
	}

	fmt.Fprintln(out, "\nKategori başına isabet:")
	for _, c := range categories {
		if p, ok := perCategory[c.Key]; ok {
			fmt.Fprintf(out, "  %-14s %d/%d\n", c.Key, p[0], p[1])
		}
	}

	fmt.Fprintln(out, "\nKarışıklık tablosu (satır: beklenen, sütun: bulunan; hata: yanıt alınamadı; nokta: sıfır)")
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	header := []string{"beklenen \\ bulunan"}
	for _, c := range categories {
		header = append(header, c.Key)
	}
	fmt.Fprintln(table, strings.Join(append(header, errorColumn), "\t"))
	for _, c := range categories {
		row, ok := counts[c.Key]
		if !ok {
			continue
		}
		cells := []string{c.Key}
		for _, found := range append(keysOf(categories), errorColumn) {
			cell := "."
			if n := row[found]; n > 0 {
				cell = strconv.Itoa(n)
			}
			cells = append(cells, cell)
		}
		fmt.Fprintln(table, strings.Join(cells, "\t"))
	}
	table.Flush()
}

// keysOf returns the keys of the categories, in order.
func keysOf(categories []ai.Category) []string {
	keys := make([]string, len(categories))
	for i, c := range categories {
		keys[i] = c.Key
	}
	return keys
}

// median returns the middle one of the durations, or the mean of the two middle
// ones; zero for none. The argument is not changed.
func median(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(durations))
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// seconds formats a duration as seconds with one decimal and the Turkish
// comma: 0,4.
func seconds(d time.Duration) string {
	return strings.Replace(strconv.FormatFloat(d.Seconds(), 'f', 1, 64), ".", ",", 1)
}

// percent formats hits as a percentage of total with one decimal and the
// Turkish comma: 90,5.
func percent(hits, total int) string {
	if total == 0 {
		return "0,0"
	}
	return strings.Replace(strconv.FormatFloat(float64(hits)*100/float64(total), 'f', 1, 64), ".", ",", 1)
}
