package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"todoapp/internal/ai"
)

const (
	benchKey = "bench-secret-key-123"
	// The set of the repository, read from the directory of this package.
	repoSet = "../../internal/ai/testdata/benchmark.json"
)

// fakeGemini is a Gemini API on localhost that answers from the oracle (the
// title -> category of the set) unless the test says otherwise. It never goes
// near the real service.
type fakeGemini struct {
	srv    *httptest.Server
	mu     sync.Mutex
	times  []time.Time
	titles []string
	// answer returns the category to give to the todo, or an HTTP status to
	// answer with instead (0: a normal answer). n counts the calls from 1.
	answer func(n int, title string) (key string, status int)
}

func newFakeGemini(t *testing.T, oracle map[string]string) *fakeGemini {
	t.Helper()
	f := &fakeGemini{}
	f.answer = func(n int, title string) (string, int) { return oracle[title], 0 }
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Contents []struct {
				Parts []struct{ Text string } `json:"parts"`
			} `json:"contents"`
		}
		json.Unmarshal(body, &req)
		userText := req.Contents[0].Parts[0].Text // "Başlık: <title>[\nAçıklama: ...]"
		title, _, _ := strings.Cut(strings.TrimPrefix(userText, "Başlık: "), "\nAçıklama: ")

		f.mu.Lock()
		f.times = append(f.times, time.Now())
		f.titles = append(f.titles, title)
		n := len(f.titles)
		f.mu.Unlock()

		key, status := f.answer(n, title)
		if status != 0 {
			// An error page that repeats the key, the way a real one can.
			w.WriteHeader(status)
			io.WriteString(w, `{"error":{"message":"problem with key `+r.Header.Get("x-goog-api-key")+`"}}`)
			return
		}
		text, _ := json.Marshal(map[string]string{"category": key, "reason": "Gerekçe: " + title})
		answer, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": string(text)}}},
			"finishReason": "STOP",
		}}})
		w.Write(answer)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGemini) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.titles)
}

// env returns the environment of a run against the fake.
func (f *fakeGemini) env(extra map[string]string) func(string) string {
	vars := map[string]string{
		"GEMINI_API_KEY":  benchKey,
		"GEMINI_BASE_URL": f.srv.URL,
		"AI_MIN_INTERVAL": "0s",
		"AI_TIMEOUT":      "5s",
	}
	for k, v := range extra {
		vars[k] = v
	}
	return func(key string) string { return vars[key] }
}

// oracleOf reads the set and returns title -> expected category.
func oracleOf(t *testing.T, path string) map[string]string {
	t.Helper()
	samples, err := readSamples(path)
	if err != nil {
		t.Fatalf("read the set %s: %v", path, err)
	}
	oracle := map[string]string{}
	for _, s := range samples {
		oracle[s.Title] = s.Expected
	}
	return oracle
}

// writeSet writes a set of samples to a file in a temp directory.
func writeSet(t *testing.T, samples ...sample) string {
	t.Helper()
	raw, err := json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "set.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runTool(t *testing.T, env func(string) string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(t.Context(), args, env, &out)
	if strings.Contains(out.String(), benchKey) || (err != nil && strings.Contains(err.Error(), benchKey)) {
		t.Errorf("the API key shows in the output or the error:\n%s\n%v", out.String(), err)
	}
	return out.String(), err
}

// The whole tool against a fake service that is right except twice: the report
// has the accuracy, the wrong ones, the table of what was mistaken for what, and
// the median time.
func TestRunReport(t *testing.T) {
	oracle := oracleOf(t, repoSet)
	fake := newFakeGemini(t, oracle)
	fake.answer = func(n int, title string) (string, int) {
		switch title {
		case "Hediye":
			return "other", 0 // research taken for other
		case "Yeni telefon":
			return "purchase", 0 // research taken for purchase
		}
		return oracle[title], 0
	}

	out, err := runTool(t, fake.env(nil), "-set", repoSet)

	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if got := fake.calls(); got != len(oracle) {
		t.Errorf("%d calls, want one for each of the %d samples", got, len(oracle))
	}
	// Two of the samples are answered wrong; the rest right. Counted from the set,
	// so that adding samples to it does not break this test.
	hits := len(oracle) - 2
	for _, want := range []string{
		"Model: gemini-3.5-flash-lite",
		fmt.Sprintf("İsabet: %d/%d (%%%s)", hits, len(oracle), percent(hits, len(oracle))),
		"hedef %85: ULAŞILDI",
		"Medyan süre:",
		"Hediye",
		"Yeni telefon",
		"beklenen: research, bulunan: other",
		"beklenen: research, bulunan: purchase",
		"Karışıklık tablosu",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
	// One line for every sample as it is measured.
	if n := strings.Count(out, "] OK "); n != hits {
		t.Errorf("%d lines marked OK, want %d", n, hits)
	}
	if n := strings.Count(out, "] YANLIŞ "); n != 2 {
		t.Errorf("%d lines marked YANLIŞ, want 2", n)
	}
	// The row of research in the table: 6 right, 1 taken for purchase, 1 for other.
	// The columns are the categories in column order, then "hata"; a dot is zero.
	wantRow := []string{"research", "1", "6", ".", ".", ".", ".", ".", "1", "."}
	var found bool
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) == len(wantRow) && fields[0] == "research" {
			found = true
			if !slices.Equal(fields, wantRow) {
				t.Errorf("row of research = %q, want %q", fields, wantRow)
			}
		}
	}
	if !found {
		t.Errorf("no row for research in the table:\n%s", out)
	}
}

func TestRunBelowTarget(t *testing.T) {
	oracle := oracleOf(t, repoSet)
	fake := newFakeGemini(t, oracle)
	fake.answer = func(n int, title string) (string, int) { return "other", 0 } // one answer for everything

	out, err := runTool(t, fake.env(nil), "-set", repoSet)

	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "hedef %85: ALTINDA") {
		t.Errorf("the report does not say that the target was missed:\n%s", out)
	}
}

func TestRunPacesTheCalls(t *testing.T) {
	const interval = 60 * time.Millisecond
	set := writeSet(t,
		sample{Title: "Süt al", Expected: "purchase"},
		sample{Title: "Annemi ara", Expected: "communication"},
		sample{Title: "Kirayı öde", Expected: "payment"})
	fake := newFakeGemini(t, map[string]string{"Süt al": "purchase", "Annemi ara": "communication", "Kirayı öde": "payment"})

	if _, err := runTool(t, fake.env(map[string]string{"AI_MIN_INTERVAL": interval.String()}), "-set", set); err != nil {
		t.Fatalf("run: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.times) != 3 {
		t.Fatalf("%d calls, want 3", len(fake.times))
	}
	const slack = 10 * time.Millisecond
	for i := 1; i < len(fake.times); i++ {
		if gap := fake.times[i].Sub(fake.times[i-1]); gap < interval-slack {
			t.Errorf("call %d came %v after call %d, want at least %v", i+1, gap, i, interval)
		}
	}
}

func TestRunWithoutKey(t *testing.T) {
	fake := newFakeGemini(t, nil)

	out, err := runTool(t, fake.env(map[string]string{"GEMINI_API_KEY": ""}), "-set", repoSet)

	if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Errorf("err = %v, want it to say that GEMINI_API_KEY is not set", err)
	}
	if out != "" {
		t.Errorf("output = %q, want none", out)
	}
	if fake.calls() != 0 {
		t.Errorf("%d calls without a key", fake.calls())
	}
}

// A setting that is wrong would be wrong for every sample: the run stops at the
// first one instead of using up the quota on forty failures.
func TestRunStopsAtASetupError(t *testing.T) {
	oracle := oracleOf(t, repoSet)
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		fake := newFakeGemini(t, oracle)
		fake.answer = func(n int, title string) (string, int) { return "", status }

		out, err := runTool(t, fake.env(nil), "-set", repoSet)

		if want := "HTTP " + strconv.Itoa(status); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("status %d: err = %v, want an error that names %q", status, err, want)
		}
		if fake.calls() != 1 {
			t.Errorf("status %d: %d calls, want the run to stop after the first", status, fake.calls())
		}
		if !strings.Contains(out, "ölçüm tamamlanamadı") {
			t.Errorf("status %d: the report does not say that the run was cut short:\n%s", status, out)
		}
		// The body of the answer says which field or key the service refused, so
		// the report shows it, on one line, with the key blanked (runTool checks
		// that the key itself is nowhere).
		if want := "problem with key ***"; !strings.Contains(out, want) {
			t.Errorf("status %d: the report lacks the body of the answer (%q):\n%s", status, want, out)
		}
	}
}

func TestRunStopsWhenTheQuotaIsUsedUp(t *testing.T) {
	oracle := oracleOf(t, repoSet)
	fake := newFakeGemini(t, oracle)
	fake.answer = func(n int, title string) (string, int) {
		if n > 3 {
			return "", http.StatusTooManyRequests
		}
		return oracle[title], 0
	}

	out, err := runTool(t, fake.env(nil), "-set", repoSet)

	if err == nil || !strings.Contains(err.Error(), "quota") {
		t.Errorf("err = %v, want it to name the quota", err)
	}
	if fake.calls() != 4 {
		t.Errorf("%d calls, want 4: three answers, then the refusal", fake.calls())
	}
	// What was measured before is reported, and why the run stopped, with the
	// body of the refusal (it says which quota ran out).
	if !strings.Contains(out, "İsabet: 3/3 (%100,0)") || !strings.Contains(out, "ölçüm tamamlanamadı") {
		t.Errorf("the partial report is missing:\n%s", out)
	}
	if !strings.Contains(out, "problem with key ***") {
		t.Errorf("the report lacks the body of the refusal:\n%s", out)
	}
}

// Anything that may pass by itself counts as a miss for that sample and the run
// goes on.
func TestRunGoesOnAfterATransientError(t *testing.T) {
	set := writeSet(t,
		sample{Title: "Süt al", Expected: "purchase"},
		sample{Title: "Annemi ara", Expected: "communication"},
		sample{Title: "Kirayı öde", Expected: "payment"})
	fake := newFakeGemini(t, map[string]string{"Süt al": "purchase", "Annemi ara": "communication", "Kirayı öde": "payment"})
	fake.answer = func(n int, title string) (string, int) {
		switch n {
		case 1:
			return "", http.StatusInternalServerError
		case 2:
			return "no-such-category", 0 // an answer that is not usable
		}
		return map[string]string{"Kirayı öde": "payment"}[title], 0
	}

	out, err := runTool(t, fake.env(nil), "-set", set)

	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if fake.calls() != 3 {
		t.Errorf("%d calls, want 3", fake.calls())
	}
	for _, want := range []string{"İsabet: 1/3 (%33,3)", "] HATA ", "Hata (yanıt alınamayan): 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}

func TestRunRejectsABadSet(t *testing.T) {
	fake := newFakeGemini(t, nil)
	tests := []struct {
		name string
		path func() string
		want string
	}{
		{"unknown category", func() string { return writeSet(t, sample{Title: "x", Expected: "xyz"}) }, `"xyz"`},
		{"the inbox", func() string { return writeSet(t, sample{Title: "x", Expected: "inbox"}) }, `"inbox"`},
		{"no title", func() string { return writeSet(t, sample{Title: " ", Expected: "work"}) }, "başlık"},
		{"empty set", func() string { return writeSet(t) }, "boş"},
		{"missing file", func() string { return filepath.Join(t.TempDir(), "yok.json") }, "yok.json"},
		{"not JSON", func() string {
			p := filepath.Join(t.TempDir(), "bozuk.json")
			os.WriteFile(p, []byte("{"), 0o644)
			return p
		}, "bozuk.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runTool(t, fake.env(nil), "-set", tt.path())

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
	if fake.calls() != 0 {
		t.Errorf("%d calls for sets that are not valid", fake.calls())
	}
}

func TestRunRejectsBadSettings(t *testing.T) {
	fake := newFakeGemini(t, nil)

	_, err := runTool(t, fake.env(map[string]string{"AI_TIMEOUT": "20"}), "-set", repoSet)

	if err == nil || !strings.Contains(err.Error(), "AI_TIMEOUT") {
		t.Errorf("err = %v, want it to name AI_TIMEOUT", err)
	}
}

// Ctrl+C in the middle of a call, or while the tool waits for its turn, stops
// the run and still reports what there is.
func TestRunStopsWhenInterrupted(t *testing.T) {
	oracle := oracleOf(t, repoSet)

	t.Run("during a call", func(t *testing.T) {
		fake := newFakeGemini(t, oracle)
		ctx, cancel := context.WithCancel(t.Context())
		fake.answer = func(n int, title string) (string, int) {
			if n == 2 {
				cancel() // the second call is in flight
			}
			return oracle[title], 0
		}
		var out bytes.Buffer

		err := run(ctx, []string{"-set", repoSet}, fake.env(nil), &out)

		if err == nil {
			t.Error("run succeeded after the interrupt, want an error")
		}
		if n := fake.calls(); n != 2 {
			t.Errorf("%d calls, want 2: the run stops when the second is interrupted", n)
		}
		if !strings.Contains(out.String(), "ölçüm tamamlanamadı") || !strings.Contains(out.String(), "İsabet: 1/1 (%100,0)") {
			t.Errorf("the report lacks the note and the one result that was measured:\n%s", out.String())
		}
	})

	t.Run("while waiting for the turn", func(t *testing.T) {
		fake := newFakeGemini(t, oracle)
		ctx, cancel := context.WithCancel(t.Context())
		fake.answer = func(n int, title string) (string, int) {
			time.AfterFunc(50*time.Millisecond, cancel) // the tool is then waiting for an hour
			return oracle[title], 0
		}
		var out bytes.Buffer

		begin := time.Now()
		err := run(ctx, []string{"-set", repoSet}, fake.env(map[string]string{"AI_MIN_INTERVAL": "1h"}), &out)

		if err == nil {
			t.Error("run succeeded after the interrupt, want an error")
		}
		if d := time.Since(begin); d > 5*time.Second {
			t.Errorf("run took %v to stop", d)
		}
		if n := fake.calls(); n != 1 {
			t.Errorf("%d calls, want 1", n)
		}
		if !strings.Contains(out.String(), "ölçüm tamamlanamadı") {
			t.Errorf("the report does not say that the run was cut short:\n%s", out.String())
		}
	})
}

// A failure is shown on one line, with the start of the body the service sent,
// whatever its layout.
func TestDescribeError(t *testing.T) {
	long := strings.Repeat("ğ", 1000)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"plain", errors.New("connection refused"), "connection refused"},
		{"http without a body", &ai.HTTPError{Status: 500}, "HTTP 500 Internal Server Error"},
		{"http with a body", &ai.HTTPError{Status: 400, Body: "{\n  \"error\": {\n    \"message\": \"Unknown name\"\n  }\n}"},
			`HTTP 400 Bad Request: { "error": { "message": "Unknown name" } }`},
		{"quota with a body", &ai.QuotaError{RetryAfter: "30", Body: "kota\ndoldu"}, "quota exceeded (HTTP 429), retry after 30: kota doldu"},
		{"wrapped", fmt.Errorf("call: %w", &ai.HTTPError{Status: 403, Body: "yasak"}), "call: HTTP 403 Forbidden: yasak"},
	}
	for _, tc := range tests {
		if got := describeError(tc.err); got != tc.want {
			t.Errorf("%s: describeError = %q, want %q", tc.name, got, tc.want)
		}
	}

	got := describeError(&ai.HTTPError{Status: 500, Body: long})
	if !strings.HasSuffix(got, "…") || len(got) > 700 || !utf8.ValidString(got) {
		t.Errorf("a long body: describeError gave %d bytes ending in %q, want it cut (to about 600 bytes, at a character) with …", len(got), got[max(0, len(got)-6):])
	}
}

func TestMedian(t *testing.T) {
	ms := func(vs ...int) []time.Duration {
		var out []time.Duration
		for _, v := range vs {
			out = append(out, time.Duration(v)*time.Millisecond)
		}
		return out
	}
	tests := []struct {
		in   []time.Duration
		want time.Duration
	}{
		{nil, 0},
		{ms(700), 700 * time.Millisecond},
		{ms(900, 100, 500), 500 * time.Millisecond},
		{ms(400, 100, 900, 200), 300 * time.Millisecond}, // the mean of the middle two
	}
	for _, tc := range tests {
		in := slices.Clone(tc.in)
		if got := median(tc.in); got != tc.want {
			t.Errorf("median(%v) = %v, want %v", in, got, tc.want)
		}
		if !slices.Equal(in, tc.in) {
			t.Errorf("median reordered its argument: %v -> %v", in, tc.in)
		}
	}
}

func TestFormatNumbers(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0,0"}, {400 * time.Millisecond, "0,4"}, {1260 * time.Millisecond, "1,3"}, {12960 * time.Millisecond, "13,0"},
	} {
		if got := seconds(tc.d); got != tc.want {
			t.Errorf("seconds(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
	for _, tc := range []struct {
		hits, total int
		want        string
	}{
		{0, 0, "0,0"}, {3, 3, "100,0"}, {1, 3, "33,3"}, {38, 42, "90,5"}, {0, 5, "0,0"},
	} {
		if got := percent(tc.hits, tc.total); got != tc.want {
			t.Errorf("percent(%d, %d) = %q, want %q", tc.hits, tc.total, got, tc.want)
		}
	}
}
