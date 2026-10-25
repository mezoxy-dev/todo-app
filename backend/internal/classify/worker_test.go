package classify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"todoapp/internal/ai"
	"todoapp/internal/applog"
	"todoapp/internal/db"
)

const (
	testModel = "gemini-test-model"
	testKey   = "sk-test-0123456789"
)

func TestMain(m *testing.M) {
	// Everything the worker logs is also written with slog; keep that out of the
	// test output.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// ---- helpers ----

func ptr[T any](v T) *T { return &v }

// newStore opens a database in a temp directory, closed when the test ends. A
// worker started later by start is stopped before that.
func newStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "worker.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	return store
}

// categoriesByKey returns the categories of the database by key.
func categoriesByKey(t *testing.T, store *db.Store) map[string]db.Category {
	t.Helper()
	categories, err := store.ListCategories(t.Context())
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	byKey := map[string]db.Category{}
	for _, c := range categories {
		byKey[c.Key] = c
	}
	return byKey
}

// createTodos creates one pending todo per title, in that order, so the first
// title is the oldest.
func createTodos(t *testing.T, store *db.Store, titles ...string) []db.Todo {
	t.Helper()
	var todos []db.Todo
	for _, title := range titles {
		todo, err := store.CreateTodo(t.Context(), db.CreateTodoParams{Title: title})
		if err != nil {
			t.Fatalf("CreateTodo(%s): %v", title, err)
		}
		todos = append(todos, todo)
	}
	return todos
}

func mustGet(t *testing.T, store *db.Store, id int64) db.Todo {
	t.Helper()
	todo, err := store.GetTodo(t.Context(), id)
	if err != nil {
		t.Fatalf("GetTodo(%d): %v", id, err)
	}
	return todo
}

// config returns a worker configuration with short times; the tests change what
// they are about.
func config(c ai.Classifier) Config {
	return Config{Classifier: c, Model: testModel, Timeout: 5 * time.Second}
}

// start runs the worker in a goroutine until the test ends (or until stop is
// called, which also waits for Run to return).
func start(t *testing.T, w *Worker) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	stop = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after its context was cancelled")
		}
	}
	t.Cleanup(stop)
	return stop
}

// waitFor polls cond until it is true; the test fails if it takes too long.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// classificationIs returns a condition: the todo has that classification.
func classificationIs(t *testing.T, store *db.Store, id int64, want string) func() bool {
	return func() bool {
		todo, err := store.GetTodo(t.Context(), id)
		return err == nil && todo.Classification == want
	}
}

// entries returns every entry of the log.
func entries(logs *applog.Buffer) []applog.Entry {
	all, _, _ := logs.After(0, 500)
	return all
}

// find returns the first entry with that message.
func find(logs *applog.Buffer, message string) (applog.Entry, bool) {
	for _, e := range entries(logs) {
		if e.Message == message {
			return e, true
		}
	}
	return applog.Entry{}, false
}

func hasEntry(logs *applog.Buffer, message string) func() bool {
	return func() bool { _, ok := find(logs, message); return ok }
}

// messages returns the messages of the log, in order.
func messages(logs *applog.Buffer) []string {
	var out []string
	for _, e := range entries(logs) {
		out = append(out, e.Message)
	}
	return out
}

// logText returns everything the log holds as one text, to look for a secret.
func logText(logs *applog.Buffer) string {
	var b strings.Builder
	for _, e := range entries(logs) {
		b.WriteString(e.Message + "\n" + e.Details + "\n")
	}
	return b.String()
}

// storedText returns the title, description and metadata of every todo as one text.
func storedText(t *testing.T, store *db.Store) string {
	t.Helper()
	todos, err := store.ListTodos(t.Context())
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}
	var b strings.Builder
	for _, todo := range todos {
		b.WriteString(todo.Title + "\n" + todo.Description + "\n" + string(todo.Metadata) + "\n")
	}
	return b.String()
}

// metadataAI returns metadata.ai of the todo, decoded.
func metadataAI(t *testing.T, todo db.Todo) map[string]any {
	t.Helper()
	var metadata struct {
		AI map[string]any `json:"ai"`
	}
	if err := json.Unmarshal(todo.Metadata, &metadata); err != nil {
		t.Fatalf("metadata %s: %v", todo.Metadata, err)
	}
	return metadata.AI
}

// wantAt checks that metadata.ai.at is a UTC timestamp of about now.
func wantAt(t *testing.T, meta map[string]any) {
	t.Helper()
	s, _ := meta["at"].(string)
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || !strings.HasSuffix(s, "Z") {
		t.Errorf("metadata.ai.at = %q, want an RFC 3339 timestamp in UTC (%v)", s, err)
		return
	}
	if d := time.Since(at); d < -time.Minute || d > time.Minute {
		t.Errorf("metadata.ai.at = %q is %v away from now", s, d)
	}
}

// call is one call to the fake classifier.
type call struct {
	at time.Time
	in ai.Input
}

// fakeClassifier records its calls and answers with fn.
type fakeClassifier struct {
	fn    func(ctx context.Context, in ai.Input) (ai.Result, error)
	mu    sync.Mutex
	calls []call
}

func (f *fakeClassifier) Classify(ctx context.Context, in ai.Input) (ai.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{at: time.Now(), in: in})
	f.mu.Unlock()
	return f.fn(ctx, in)
}

func (f *fakeClassifier) recorded() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeClassifier) titles() []string {
	var titles []string
	for _, c := range f.recorded() {
		titles = append(titles, c.in.Title)
	}
	return titles
}

// answers returns a classifier that puts every title in the category the map
// names, with a reason that names the title.
func answers(byTitle map[string]string) *fakeClassifier {
	return &fakeClassifier{fn: func(ctx context.Context, in ai.Input) (ai.Result, error) {
		key := byTitle[in.Title]
		reason := "Gerekçe: " + in.Title
		raw, _ := json.Marshal(map[string]string{"category": key, "reason": reason})
		return ai.Result{Key: key, Reason: reason, Model: testModel, Raw: string(raw)}, nil
	}}
}

// blocking returns a classifier that waits in its first call until release is
// closed, and tells the test when that call has begun. Then it answers with fn.
func blocking(started chan<- struct{}, release <-chan struct{}, fn func(in ai.Input) (ai.Result, error)) *fakeClassifier {
	var once sync.Once
	return &fakeClassifier{fn: func(ctx context.Context, in ai.Input) (ai.Result, error) {
		once.Do(func() {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
		if err := ctx.Err(); err != nil {
			return ai.Result{}, err
		}
		return fn(in)
	}}
}

// ---- tests ----

// Step 3 spec 6.4: at start-up the pending todos are queued, the oldest first,
// and each one is classified in turn.
func TestWorkerClassifiesPendingInOrder(t *testing.T) {
	store := newStore(t)
	todos := createTodos(t, store, "Süt al", "Tatil için otel bak", "Elektrik faturasını öde")
	// The first has a description, and goes to the model with it.
	if _, err := store.UpdateTodo(t.Context(), todos[0].ID, db.UpdateTodoParams{Description: ptr("2 litre")}); err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}
	fake := answers(map[string]string{"Süt al": "purchase", "Tatil için otel bak": "research", "Elektrik faturasını öde": "payment"})
	logs := applog.New(500)
	start(t, NewWorker(store, logs, config(fake)))

	for _, todo := range todos {
		waitFor(t, todo.Title+" to be classified", classificationIs(t, store, todo.ID, db.ClassificationDone))
	}

	// Oldest first.
	if want := []string{"Süt al", "Tatil için otel bak", "Elektrik faturasını öde"}; !slices.Equal(fake.titles(), want) {
		t.Errorf("calls in the order %q, want %q", fake.titles(), want)
	}
	byKey := categoriesByKey(t, store)
	for i, key := range []string{"purchase", "research", "payment"} {
		got := mustGet(t, store, todos[i].ID)
		if got.CategoryID != byKey[key].ID || got.Position != 0 {
			t.Errorf("%s: category %d, position %d; want the top of %s (%d)", got.Title, got.CategoryID, got.Position, key, byKey[key].ID)
		}
		meta := metadataAI(t, got)
		if meta["model"] != testModel || meta["category"] != key || meta["reason"] != "Gerekçe: "+got.Title || meta["duration_ms"] != float64(0) {
			t.Errorf("%s: metadata.ai = %v, want model, category, reason and duration_ms of the answer", got.Title, meta)
		}
		wantAt(t, meta)
	}

	// What the model is given: the title, the description if there is one, and
	// the categories of the database without the inbox, in column order.
	calls := fake.recorded()
	if calls[0].in.Description != "2 litre" || calls[1].in.Description != "" {
		t.Errorf("descriptions %q and %q, want %q and none", calls[0].in.Description, calls[1].in.Description, "2 litre")
	}
	var keys []string
	for _, c := range calls[0].in.Categories {
		keys = append(keys, c.Key)
		if want := byKey[c.Key]; c.Name != want.Name || c.Description != want.Description {
			t.Errorf("category %s given as %+v, want the name and description of the database: %+v", c.Key, c, want)
		}
	}
	if want := []string{"purchase", "research", "communication", "payment", "appointment", "work", "home", "other"}; !slices.Equal(keys, want) {
		t.Errorf("categories given to the model = %v, want %v (no inbox)", keys, want)
	}

	// The log: the queue at start-up, then for each todo "Sınıflandırılıyor"
	// and "Sınıflandırıldı" (step 3 spec 8).
	want := []string{
		"3 kart sınıflandırma kuyruğuna alındı",
		"Sınıflandırılıyor: “Süt al”",
		"Sınıflandırıldı: “Süt al” → Satın alım (0,0 sn)",
		"Sınıflandırılıyor: “Tatil için otel bak”",
		"Sınıflandırıldı: “Tatil için otel bak” → Araştırma (0,0 sn)",
		"Sınıflandırılıyor: “Elektrik faturasını öde”",
		"Sınıflandırıldı: “Elektrik faturasını öde” → Ödeme (0,0 sn)",
	}
	if got := messages(logs); !slices.Equal(got, want) {
		t.Errorf("log messages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, e := range entries(logs) {
		wantSource, wantLevel := applog.SourceAI, applog.LevelInfo
		if strings.HasSuffix(e.Message, "kuyruğuna alındı") {
			wantSource = applog.SourceSystem
		}
		if e.Source != wantSource || e.Level != wantLevel {
			t.Errorf("%q: source %s, level %s; want %s, %s", e.Message, e.Source, e.Level, wantSource, wantLevel)
		}
		if strings.HasPrefix(e.Message, "Sınıflandırıldı") != (e.Details != "") {
			t.Errorf("%q: details %q; want details on the result only", e.Message, e.Details)
		}
	}
	done, _ := find(logs, "Sınıflandırıldı: “Süt al” → Satın alım (0,0 sn)")
	for _, part := range []string{
		"Model: " + testModel,
		"Başlık: Süt al\nAçıklama: 2 litre",                  // the text that was sent
		`{"category":"purchase","reason":"Gerekçe: Süt al"}`, // the JSON the model answered with
		"Gerekçe: Gerekçe: Süt al",
		"Süre: 0,0 sn",
	} {
		if !strings.Contains(done.Details, part) {
			t.Errorf("details of the result lack %q:\n%s", part, done.Details)
		}
	}
}

func TestWorkerQueuesTodosThatArriveLater(t *testing.T) {
	store := newStore(t)
	fake := answers(map[string]string{"Ekmek al": "purchase", "Annemi ara": "communication"})
	w := NewWorker(store, applog.New(500), config(fake))
	start(t, w)

	for _, title := range []string{"Ekmek al", "Annemi ara"} {
		todo := createTodos(t, store, title)[0]
		w.Enqueue(todo.ID)
		waitFor(t, title+" to be classified", classificationIs(t, store, todo.ID, db.ClassificationDone))
	}
}

// With nothing pending, the start-up has nothing to report: no "0 kart ..."
// line in the log. Calling queuePending directly keeps the test free of any race
// with the goroutine of Run.
func TestWorkerQueuesNothingWhenNothingIsPending(t *testing.T) {
	store := newStore(t)
	logs := applog.New(500)
	w := NewWorker(store, logs, config(answers(nil)))

	w.queuePending(t.Context())

	if got := messages(logs); len(got) != 0 {
		t.Errorf("log = %q, want no entry when nothing is pending", got)
	}
	if len(w.queue) != 0 {
		t.Errorf("queue = %v, want it empty", w.queue)
	}

	// The same call with two failed todos and one pending: only that one is queued.
	todos := createTodos(t, store, "Bir", "İki", "Üç")
	for _, todo := range todos[:2] {
		if _, _, err := store.FailClassification(t.Context(), todo.ID, json.RawMessage(`{"model":"m","error":"e","at":"x"}`)); err != nil {
			t.Fatalf("FailClassification: %v", err)
		}
	}
	w.queuePending(t.Context())

	if want := []int64{todos[2].ID}; !slices.Equal(w.queue, want) {
		t.Errorf("queue = %v, want only the pending todo %v", w.queue, want)
	}
	if got, want := messages(logs), []string{"1 kart sınıflandırma kuyruğuna alındı"}; !slices.Equal(got, want) {
		t.Errorf("log = %q, want %q", got, want)
	}
}

// Step 3 spec 6.4: at least AI_MIN_INTERVAL between two calls.
func TestWorkerRespectsMinInterval(t *testing.T) {
	const interval = 80 * time.Millisecond
	store := newStore(t)
	todos := createTodos(t, store, "A", "B", "C")
	fake := answers(map[string]string{"A": "work", "B": "work", "C": "work"})
	cfg := config(fake)
	cfg.MinInterval = interval
	startedAt := time.Now()
	start(t, NewWorker(store, applog.New(500), cfg))

	for _, todo := range todos {
		waitFor(t, todo.Title+" to be classified", classificationIs(t, store, todo.ID, db.ClassificationDone))
	}

	calls := fake.recorded()
	if len(calls) != 3 {
		t.Fatalf("%d calls, want 3", len(calls))
	}
	// The pause is taken from the start of the previous call, and the two times
	// are noted in different places, hence a little room.
	const slack = 10 * time.Millisecond
	for i := 1; i < len(calls); i++ {
		if gap := calls[i].at.Sub(calls[i-1].at); gap < interval-slack {
			t.Errorf("call %d came %v after call %d, want at least %v", i+1, gap, i, interval)
		}
	}
	if total := time.Since(startedAt); total < 2*interval-slack {
		t.Errorf("three calls took %v, want at least two intervals (%v)", total, 2*interval)
	}
}

// Step 3 spec 6.4 and 6.5: without a key the todos fail at once, whatever the
// interval, and the app goes on working.
func TestWorkerWithoutClassifierFailsCards(t *testing.T) {
	store := newStore(t)
	todos := createTodos(t, store, "A", "B")
	cfg := config(nil)
	cfg.MinInterval = time.Hour // a worker that waited for it would not get done
	logs := applog.New(500)
	w := NewWorker(store, logs, cfg)
	start(t, w)

	for _, todo := range todos {
		waitFor(t, todo.Title+" to fail", classificationIs(t, store, todo.ID, db.ClassificationFailed))
	}
	// One that arrives later fails just the same.
	late := createTodos(t, store, "C")[0]
	w.Enqueue(late.ID)
	waitFor(t, "C to fail", classificationIs(t, store, late.ID, db.ClassificationFailed))

	inbox := categoriesByKey(t, store)[db.InboxKey].ID
	for _, todo := range append(todos, late) {
		got := mustGet(t, store, todo.ID)
		if got.CategoryID != inbox {
			t.Errorf("%s: category %d, want it to stay in the inbox (%d)", got.Title, got.CategoryID, inbox)
		}
		meta := metadataAI(t, got)
		if meta["error"] != "API anahtarı yok" || meta["model"] != testModel {
			t.Errorf("%s: metadata.ai = %v, want the error %q and the model", got.Title, meta, "API anahtarı yok")
		}
		wantAt(t, meta)

		entry, ok := find(logs, "Sınıflandırılamadı: “"+got.Title+"”")
		if !ok {
			t.Errorf("%s: no log entry about the failure; log: %v", got.Title, messages(logs))
			continue
		}
		if entry.Level != applog.LevelError || entry.Source != applog.SourceAI {
			t.Errorf("%s: failure logged as %s/%s, want error/ai", got.Title, entry.Level, entry.Source)
		}
		if !strings.Contains(entry.Details, "API anahtarı yok") || !strings.Contains(entry.Details, "GEMINI_API_KEY") {
			t.Errorf("%s: details = %q, want them to say that there is no key", got.Title, entry.Details)
		}
	}
	// It did not even look as if a call was made.
	for _, m := range messages(logs) {
		if strings.HasPrefix(m, "Sınıflandırılıyor") {
			t.Errorf("log has %q, but there is nothing to call", m)
		}
	}
}

// Review Focus 2: quota, errors and invalid answers leave the card failed in the
// inbox, with the details in the log; the worker goes on and does not try again.
func TestWorkerQuotaAndErrors(t *testing.T) {
	failures := []struct {
		title   string
		err     error
		details []string // what the log entry shows
		short   string   // what metadata.ai.error shows
	}{
		{"quota", &ai.QuotaError{RetryAfter: "30", Body: `{"error":{"status":"RESOURCE_EXHAUSTED"}}`},
			[]string{"kota aşıldı", "429", "Retry-After: 30", "RESOURCE_EXHAUSTED"}, "kota aşıldı (HTTP 429)"},
		{"quota without delay", &ai.QuotaError{},
			[]string{"kota aşıldı", "429"}, "kota aşıldı (HTTP 429)"},
		{"server error", &ai.HTTPError{Status: 500, Body: "boom"},
			[]string{"HTTP hatası", "500", "boom"}, "HTTP hatası (500)"},
		{"bad answer", fmt.Errorf("%w: category %q is not one of the 8 that were offered", ai.ErrBadResponse, "xyz"),
			[]string{"geçersiz yanıt", `"xyz"`}, "geçersiz yanıt"},
		{"timeout", fmt.Errorf("call gemini: %w", context.DeadlineExceeded),
			[]string{"zaman aşımı"}, "zaman aşımı (5s)"},
		{"connection", errors.New("call gemini: dial tcp 127.0.0.1:1: connect: connection refused"),
			[]string{"connection refused"}, "call gemini: dial tcp 127.0.0.1:1: connect: connection refused"},
	}
	store := newStore(t)
	var titles []string
	for _, f := range failures {
		titles = append(titles, f.title)
	}
	todos := createTodos(t, store, titles...)
	byTitle := map[string]error{}
	for _, f := range failures {
		byTitle[f.title] = f.err
	}
	fake := &fakeClassifier{fn: func(ctx context.Context, in ai.Input) (ai.Result, error) {
		return ai.Result{}, byTitle[in.Title]
	}}
	logs := applog.New(500)
	start(t, NewWorker(store, logs, config(fake)))

	// The worker does not get stuck on the first failure: every todo fails in turn.
	for _, todo := range todos {
		waitFor(t, todo.Title+" to fail", classificationIs(t, store, todo.ID, db.ClassificationFailed))
	}
	inbox := categoriesByKey(t, store)[db.InboxKey].ID
	for i, f := range failures {
		got := mustGet(t, store, todos[i].ID)
		if got.CategoryID != inbox {
			t.Errorf("%s: category %d, want it to stay in the inbox", f.title, got.CategoryID)
		}
		meta := metadataAI(t, got)
		if meta["error"] != f.short || meta["model"] != testModel {
			t.Errorf("%s: metadata.ai = %v, want error %q and the model", f.title, meta, f.short)
		}
		wantAt(t, meta)

		entry, ok := find(logs, "Sınıflandırılamadı: “"+f.title+"”")
		if !ok {
			t.Errorf("%s: no log entry about the failure", f.title)
			continue
		}
		if entry.Level != applog.LevelError || entry.Source != applog.SourceAI {
			t.Errorf("%s: logged as %s/%s, want error/ai", f.title, entry.Level, entry.Source)
		}
		for _, part := range f.details {
			if !strings.Contains(entry.Details, part) {
				t.Errorf("%s: details lack %q:\n%s", f.title, part, entry.Details)
			}
		}
	}

	// Nothing is tried again by itself: every todo was sent once, and a while
	// later still has been.
	time.Sleep(150 * time.Millisecond)
	if want := titles; !slices.Equal(fake.titles(), want) {
		t.Errorf("calls %q, want each todo once, in order: %q", fake.titles(), want)
	}
}

func TestWorkerAppliesTimeout(t *testing.T) {
	store := newStore(t)
	todo := createTodos(t, store, "Yavaş")[0]
	fake := &fakeClassifier{fn: func(ctx context.Context, in ai.Input) (ai.Result, error) {
		<-ctx.Done() // a call that never answers
		return ai.Result{}, ctx.Err()
	}}
	cfg := config(fake)
	cfg.Timeout = 50 * time.Millisecond
	logs := applog.New(500)
	start(t, NewWorker(store, logs, cfg))

	waitFor(t, "the call to time out", classificationIs(t, store, todo.ID, db.ClassificationFailed))

	entry, ok := find(logs, "Sınıflandırılamadı: “Yavaş”")
	if !ok || !strings.Contains(entry.Details, "zaman aşımı") {
		t.Errorf("log entry %+v (found %v), want a failure by time-out", entry, ok)
	}
}

// Review Focus 1: the user moves the card by hand while the model is thinking.
// The answer must not take the card back.
func TestWorkerDoesNotOverrideManualMove(t *testing.T) {
	store := newStore(t)
	todo := createTodos(t, store, "Süt al")[0]
	started, release := make(chan struct{}), make(chan struct{})
	fake := blocking(started, release, func(in ai.Input) (ai.Result, error) {
		return ai.Result{Key: "purchase", Reason: "Market alışverişi.", Model: testModel, Raw: `{"category":"purchase"}`}, nil
	})
	logs := applog.New(500)
	start(t, NewWorker(store, logs, config(fake)))
	<-started

	// The user drags the card to "Araştırma" while the call is in progress.
	research := categoriesByKey(t, store)["research"]
	if _, err := store.UpdateTodo(t.Context(), todo.ID, db.UpdateTodoParams{CategoryID: &research.ID}); err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}
	close(release)

	const message = "Öneri uygulanmadı: “Süt al” (kart elle taşınmıştı)"
	waitFor(t, "the answer to be dropped", hasEntry(logs, message))

	got := mustGet(t, store, todo.ID)
	if got.CategoryID != research.ID || got.Classification != db.ClassificationManual || got.Position != 0 {
		t.Errorf("got %+v, want the card to stay at the top of Araştırma as manual", got)
	}
	if metadataAI(t, got) != nil {
		t.Errorf("metadata = %s, want no metadata.ai: the answer was not applied", got.Metadata)
	}
	entry, _ := find(logs, message)
	if entry.Level != applog.LevelWarn || entry.Source != applog.SourceAI {
		t.Errorf("logged as %s/%s, want warn/ai", entry.Level, entry.Source)
	}
	for _, part := range []string{"Satın alım", "purchase", "Market alışverişi."} { // the category it suggested, and why
		if !strings.Contains(entry.Details, part) {
			t.Errorf("details lack %q:\n%s", part, entry.Details)
		}
	}
	for _, m := range messages(logs) {
		if strings.HasPrefix(m, "Sınıflandırıldı") {
			t.Errorf("log has %q, but the answer was not used", m)
		}
	}
}

// The same race with an error: the card the user moved is not marked failed.
func TestWorkerFailureAfterManualMoveLeavesCardAlone(t *testing.T) {
	store := newStore(t)
	todo := createTodos(t, store, "Süt al")[0]
	started, release := make(chan struct{}), make(chan struct{})
	fake := blocking(started, release, func(in ai.Input) (ai.Result, error) {
		return ai.Result{}, &ai.QuotaError{RetryAfter: "30"}
	})
	logs := applog.New(500)
	start(t, NewWorker(store, logs, config(fake)))
	<-started
	home := categoriesByKey(t, store)["home"]
	if _, err := store.UpdateTodo(t.Context(), todo.ID, db.UpdateTodoParams{CategoryID: &home.ID}); err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}
	close(release)

	waitFor(t, "the failure to be logged", hasEntry(logs, "Sınıflandırılamadı: “Süt al”"))

	got := mustGet(t, store, todo.ID)
	if got.CategoryID != home.ID || got.Classification != db.ClassificationManual || metadataAI(t, got) != nil {
		t.Errorf("got %+v, want the manual card untouched", got)
	}
	entry, _ := find(logs, "Sınıflandırılamadı: “Süt al”")
	if !strings.Contains(entry.Details, "429") || !strings.Contains(entry.Details, "elle taşınmıştı") {
		t.Errorf("details = %q, want the error and a note that the card had been moved", entry.Details)
	}
}

// A todo that was moved before its turn does not cost a call.
func TestWorkerSkipsTodosThatAreNotPending(t *testing.T) {
	store := newStore(t)
	todos := createTodos(t, store, "Birinci", "İkinci", "Üçüncü")
	started, release := make(chan struct{}), make(chan struct{})
	fake := blocking(started, release, func(in ai.Input) (ai.Result, error) {
		return ai.Result{Key: "work", Reason: "r", Model: testModel, Raw: "{}"}, nil
	})
	logs := applog.New(500)
	start(t, NewWorker(store, logs, config(fake)))
	<-started // "Birinci" is being classified; the other two wait in the queue

	other := categoriesByKey(t, store)["other"].ID
	if _, err := store.UpdateTodo(t.Context(), todos[1].ID, db.UpdateTodoParams{CategoryID: &other}); err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}
	if err := store.DeleteTodo(t.Context(), todos[2].ID); err != nil { // and one that is gone
		t.Fatalf("DeleteTodo: %v", err)
	}
	close(release)

	waitFor(t, "Birinci to be classified", classificationIs(t, store, todos[0].ID, db.ClassificationDone))
	time.Sleep(100 * time.Millisecond)
	if want := []string{"Birinci"}; !slices.Equal(fake.titles(), want) {
		t.Errorf("calls %q, want %q only", fake.titles(), want)
	}
	if got := mustGet(t, store, todos[1].ID); got.Classification != db.ClassificationManual || got.CategoryID != other {
		t.Errorf("the todo moved by hand = %+v, want it untouched", got)
	}
}

func TestWorkerEnqueueDeduplicates(t *testing.T) {
	store := newStore(t)
	todo := createTodos(t, store, "Süt al")[0]
	started, release := make(chan struct{}), make(chan struct{})
	fake := blocking(started, release, func(in ai.Input) (ai.Result, error) {
		return ai.Result{Key: "purchase", Reason: "r", Model: testModel, Raw: "{}"}, nil
	})
	w := NewWorker(store, applog.New(500), config(fake))
	start(t, w)
	<-started
	other := createTodos(t, store, "Ekmek al")[0]

	// The same todo again and again while the worker is busy.
	for range 5 {
		w.Enqueue(other.ID)
		w.Enqueue(todo.ID) // being classified now, but not waiting: it is queued once more
	}
	close(release)

	waitFor(t, "Ekmek al to be classified", classificationIs(t, store, other.ID, db.ClassificationDone))
	time.Sleep(100 * time.Millisecond)
	// "Süt al" is done after its first call, so the entry that waited is dropped
	// when it comes up; "Ekmek al" is sent once however often it was queued.
	if want := []string{"Süt al", "Ekmek al"}; !slices.Equal(fake.titles(), want) {
		t.Errorf("calls %q, want %q", fake.titles(), want)
	}
}

func TestWorkerIgnoresUnknownIDs(t *testing.T) {
	store := newStore(t)
	fake := answers(map[string]string{"Süt al": "purchase"})
	w := NewWorker(store, applog.New(500), config(fake))
	start(t, w)
	w.Enqueue(9999)
	todo := createTodos(t, store, "Süt al")[0]
	w.Enqueue(todo.ID)

	waitFor(t, "Süt al to be classified", classificationIs(t, store, todo.ID, db.ClassificationDone))

	if want := []string{"Süt al"}; !slices.Equal(fake.titles(), want) {
		t.Errorf("calls %q, want %q", fake.titles(), want)
	}
}

// "Tekrar dene": a failed todo is queued again by the API and then classified.
func TestWorkerClassifiesRequeuedTodo(t *testing.T) {
	store := newStore(t)
	todo := createTodos(t, store, "Süt al")[0]
	var mu sync.Mutex
	fail := true
	fake := &fakeClassifier{fn: func(ctx context.Context, in ai.Input) (ai.Result, error) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return ai.Result{}, &ai.QuotaError{}
		}
		return ai.Result{Key: "purchase", Reason: "r", Model: testModel, Raw: "{}"}, nil
	}}
	w := NewWorker(store, applog.New(500), config(fake))
	start(t, w)
	waitFor(t, "the first attempt to fail", classificationIs(t, store, todo.ID, db.ClassificationFailed))

	mu.Lock()
	fail = false
	mu.Unlock()
	if _, err := store.RequeueClassification(t.Context(), todo.ID); err != nil {
		t.Fatalf("RequeueClassification: %v", err)
	}
	w.Enqueue(todo.ID)

	waitFor(t, "the retry to succeed", classificationIs(t, store, todo.ID, db.ClassificationDone))
	got := mustGet(t, store, todo.ID)
	if got.CategoryID != categoriesByKey(t, store)["purchase"].ID {
		t.Errorf("category %d, want purchase", got.CategoryID)
	}
	if meta := metadataAI(t, got); meta["error"] != nil || meta["category"] != "purchase" {
		t.Errorf("metadata.ai = %v, want the answer and none of the old error", meta)
	}
}

// Review Focus 4: the key is in no log entry and in no stored metadata, even if
// the classifier's errors and answers echo it.
func TestWorkerNeverLogsAPIKey(t *testing.T) {
	t.Run("a classifier that echoes the key", func(t *testing.T) {
		store := newStore(t)
		titles := []string{"quota", "http", "other error", "answer"}
		todos := createTodos(t, store, titles...)
		fake := &fakeClassifier{fn: func(ctx context.Context, in ai.Input) (ai.Result, error) {
			switch in.Title {
			case "quota":
				return ai.Result{}, &ai.QuotaError{RetryAfter: "30", Body: `{"message":"key ` + testKey + ` is over quota"}`}
			case "http":
				return ai.Result{}, &ai.HTTPError{Status: 400, Body: "API key not valid: " + testKey}
			case "answer":
				return ai.Result{Key: "work", Reason: "the key is " + testKey, Model: testModel, Raw: `{"reason":"` + testKey + `"}`}, nil
			}
			return ai.Result{}, errors.New("call gemini: bad credentials " + testKey)
		}}
		logs := applog.New(500)
		cfg := config(fake)
		cfg.APIKey = testKey
		start(t, NewWorker(store, logs, cfg))

		for _, todo := range todos {
			waitFor(t, todo.Title+" to be dealt with", func() bool {
				return mustGet(t, store, todo.ID).Classification != db.ClassificationPending
			})
		}

		if text := logText(logs); strings.Contains(text, testKey) {
			t.Errorf("the key is in the log:\n%s", text)
		}
		if text := storedText(t, store); strings.Contains(text, testKey) {
			t.Errorf("the key is in the stored todos:\n%s", text)
		}
		// The details are still there, only the key is blanked.
		entry, _ := find(logs, "Sınıflandırılamadı: “http”")
		if !strings.Contains(entry.Details, "API key not valid") || !strings.Contains(entry.Details, "***") {
			t.Errorf("details = %q, want the message with the key blanked", entry.Details)
		}
	})

	// The real client against a server that answers every kind of failure with a
	// body that repeats the key it was sent.
	t.Run("the Gemini client against a server that echoes the key", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			status int
			body   func(key string) string
		}{
			{"400", http.StatusBadRequest, func(key string) string { return `{"error":{"message":"API key not valid: ` + key + `"}}` }},
			{"500", http.StatusInternalServerError, func(key string) string { return "internal error for key " + key }},
			{"429", http.StatusTooManyRequests, func(key string) string { return `{"error":{"message":"quota of ` + key + `"}}` }},
			{"200 with a bad category", http.StatusOK, func(key string) string {
				return `{"candidates":[{"content":{"parts":[{"text":"{\"category\":\"` + key + `\",\"reason\":\"x\"}"}]},"finishReason":"STOP"}]}`
			}},
			{"200 with text that is not JSON", http.StatusOK, func(key string) string {
				return `{"candidates":[{"content":{"parts":[{"text":"Your key is ` + key + `"}]},"finishReason":"STOP"}]}`
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					key := r.Header.Get("x-goog-api-key")
					w.WriteHeader(tc.status)
					io.WriteString(w, tc.body(key))
				}))
				t.Cleanup(srv.Close)
				store := newStore(t)
				todo := createTodos(t, store, "Süt al")[0]
				gemini := ai.NewGemini(ai.GeminiConfig{APIKey: testKey, Model: testModel, BaseURL: srv.URL, Timeout: 5 * time.Second})
				logs := applog.New(500)
				cfg := config(gemini)
				cfg.APIKey = testKey
				start(t, NewWorker(store, logs, cfg))

				waitFor(t, "the todo to fail", classificationIs(t, store, todo.ID, db.ClassificationFailed))

				if text := logText(logs); strings.Contains(text, testKey) {
					t.Errorf("the key is in the log:\n%s", text)
				}
				if text := storedText(t, store); strings.Contains(text, testKey) {
					t.Errorf("the key is in the stored todos:\n%s", text)
				}
				if entry, ok := find(logs, "Sınıflandırılamadı: “Süt al”"); !ok || entry.Details == "" {
					t.Errorf("failure entry %+v (found %v), want it with details", entry, ok)
				}
			})
		}
	})
}

// A real round trip with the Gemini client: the worker and the client agree on
// the errors, and the happy path works end to end.
func TestWorkerWithGeminiClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"{\"category\":\"communication\",\"reason\":\"Birini arama.\"}"}]},"finishReason":"STOP"}]}`)
	}))
	t.Cleanup(srv.Close)
	store := newStore(t)
	todo := createTodos(t, store, "Annemi ara")[0]
	gemini := ai.NewGemini(ai.GeminiConfig{APIKey: testKey, Model: testModel, BaseURL: srv.URL, Timeout: 5 * time.Second})
	logs := applog.New(500)
	start(t, NewWorker(store, logs, config(gemini)))

	waitFor(t, "the todo to be classified", classificationIs(t, store, todo.ID, db.ClassificationDone))

	got := mustGet(t, store, todo.ID)
	if got.CategoryID != categoriesByKey(t, store)["communication"].ID {
		t.Errorf("category %d, want communication", got.CategoryID)
	}
	meta := metadataAI(t, got)
	if meta["category"] != "communication" || meta["reason"] != "Birini arama." || meta["model"] != testModel {
		t.Errorf("metadata.ai = %v", meta)
	}
	if d, _ := meta["duration_ms"].(float64); d < 0 {
		t.Errorf("duration_ms = %v", meta["duration_ms"])
	}
}

func TestWorkerStopsOnContextCancel(t *testing.T) {
	t.Run("in the middle of a call", func(t *testing.T) {
		store := newStore(t)
		todos := createTodos(t, store, "Birinci", "İkinci")
		started := make(chan struct{})
		fake := blocking(started, make(chan struct{}), nil) // never released: the call ends with the context
		logs := applog.New(500)
		stop := start(t, NewWorker(store, logs, config(fake)))
		<-started

		begin := time.Now()
		stop() // cancels and waits, failing the test if Run does not return

		if d := time.Since(begin); d > 2*time.Second {
			t.Errorf("Run took %v to return", d)
		}
		// Shutting down is not a failure: the todos wait for the next start.
		for _, todo := range todos {
			if got := mustGet(t, store, todo.ID); got.Classification != db.ClassificationPending {
				t.Errorf("%s: classification %q, want it still pending", got.Title, got.Classification)
			}
		}
		for _, m := range messages(logs) {
			if strings.HasPrefix(m, "Sınıflandırılamadı") || strings.HasPrefix(m, "Sınıflandırıldı") {
				t.Errorf("log has %q, want nothing about a result when the worker was stopped", m)
			}
		}
		if ids, err := store.PendingTodoIDs(t.Context()); err != nil || len(ids) != 2 {
			t.Errorf("PendingTodoIDs = %v, %v; want both todos for the next start", ids, err)
		}
	})

	t.Run("while waiting for its turn", func(t *testing.T) {
		store := newStore(t)
		todos := createTodos(t, store, "Birinci", "İkinci")
		fake := answers(map[string]string{"Birinci": "work", "İkinci": "work"})
		cfg := config(fake)
		cfg.MinInterval = time.Hour
		stop := start(t, NewWorker(store, applog.New(500), cfg))
		waitFor(t, "Birinci to be classified", classificationIs(t, store, todos[0].ID, db.ClassificationDone))
		time.Sleep(50 * time.Millisecond) // the worker is now waiting for an hour

		begin := time.Now()
		stop()

		if d := time.Since(begin); d > 2*time.Second {
			t.Errorf("Run took %v to return while it waited for its turn", d)
		}
		if got := mustGet(t, store, todos[1].ID); got.Classification != db.ClassificationPending {
			t.Errorf("İkinci: classification %q, want pending", got.Classification)
		}
		if n := len(fake.recorded()); n != 1 {
			t.Errorf("%d calls, want 1", n)
		}
	})

	t.Run("when idle", func(t *testing.T) {
		store := newStore(t)
		stop := start(t, NewWorker(store, applog.New(500), config(answers(nil))))
		time.Sleep(20 * time.Millisecond)

		stop()
	})

	t.Run("before it begins", func(t *testing.T) {
		store := newStore(t)
		createTodos(t, store, "A")
		w := NewWorker(store, applog.New(500), config(answers(map[string]string{"A": "work"})))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		done := make(chan struct{})
		go func() { defer close(done); w.Run(ctx) }()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return with a context that was cancelled already")
		}
	})
}

func TestSeconds(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0,0"},
		{400 * time.Millisecond, "0,4"},
		{430 * time.Millisecond, "0,4"},
		{460 * time.Millisecond, "0,5"},
		{2 * time.Second, "2,0"},
		{12960 * time.Millisecond, "13,0"},
		{90 * time.Second, "90,0"},
	}
	for _, tc := range tests {
		if got := seconds(tc.d); got != tc.want {
			t.Errorf("seconds(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
