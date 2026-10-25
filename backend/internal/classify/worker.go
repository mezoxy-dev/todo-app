// Package classify runs the background worker that has the AI put the todos in
// their categories (step 3 spec 6.4): one goroutine takes the todos from a
// queue without duplicates, one after the other, with a pause between two calls
// so that the free quota of the service is not used up. The answer is applied
// only to a todo that nobody has touched in the meantime.
package classify

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"todoapp/internal/ai"
	"todoapp/internal/applog"
	"todoapp/internal/db"
)

// redacted stands where the API key was; ai.Gemini uses the same text.
const redacted = "***"

// Config configures a Worker.
type Config struct {
	// Classifier asks the AI. Nil means that there is no API key: every todo
	// then fails at once with "API anahtarı yok", and the app goes on working.
	Classifier ai.Classifier
	// APIKey is blanked in everything the worker writes down: the log and the
	// metadata of the todos. A Classifier keeps the key out of its errors itself;
	// this is the second line of defense, for what a service echoes and an
	// implementation lets through.
	APIKey string
	// Model is named in the metadata of the todos and in the log.
	Model string
	// Timeout is the longest a call may take; zero means no limit of the worker's own.
	Timeout time.Duration
	// MinInterval is the shortest time from the start of one call to the start
	// of the next, so that the quota per minute of the service is not exceeded.
	MinInterval time.Duration
}

// Worker classifies the todos that wait for the AI. Enqueue may be called from
// any goroutine; Run is called once.
type Worker struct {
	store *db.Store
	logs  *applog.Buffer
	cfg   Config

	mu     sync.Mutex
	queue  []int64        // ids that wait for their turn, the oldest first
	queued map[int64]bool // the same ids, to leave out a second one
	wake   chan struct{}  // holds a token when the queue may have grown

	lastCall time.Time // when the latest call began; only the goroutine of Run uses it
}

// NewWorker returns a worker that reads and changes the todos in store and
// reports to logs. Nothing happens until Run is called.
func NewWorker(store *db.Store, logs *applog.Buffer, cfg Config) *Worker {
	return &Worker{
		store:  store,
		logs:   logs,
		cfg:    cfg,
		queued: map[int64]bool{},
		wake:   make(chan struct{}, 1),
	}
}

// Enqueue adds the todo to the queue, unless it is waiting there already. A
// todo that is being classified right now is not in the queue and is added
// again, which is what "try again" needs.
func (w *Worker) Enqueue(id int64) {
	w.mu.Lock()
	if !w.queued[id] {
		w.queued[id] = true
		w.queue = append(w.queue, id)
	}
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default: // a token is there already
	}
}

// Run puts the todos that are pending into the queue, the oldest first, and
// classifies the queue one todo at a time until ctx is done. A call that is in
// progress when ctx ends is given up, and its todo stays pending: it is queued
// again by the next start.
func (w *Worker) Run(ctx context.Context) {
	w.queuePending(ctx)
	for {
		id, ok := w.next(ctx)
		if !ok {
			return
		}
		w.process(ctx, id)
	}
}

// queuePending queues the todos that are pending now, the oldest first: those
// that were not classified before the server stopped. Failed todos are not
// among them; they wait for "Tekrar dene".
func (w *Worker) queuePending(ctx context.Context) {
	ids, err := w.store.PendingTodoIDs(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("classify: list pending todos", "error", err)
		}
		return
	}
	for _, id := range ids {
		w.Enqueue(id)
	}
	if len(ids) > 0 {
		w.logs.Add(applog.LevelInfo, applog.SourceSystem, fmt.Sprintf("%d kart sınıflandırma kuyruğuna alındı", len(ids)), "")
	}
}

// next waits for the next id of the queue. It returns false once ctx is done,
// even if there is more in the queue.
func (w *Worker) next(ctx context.Context) (int64, bool) {
	for {
		if ctx.Err() != nil {
			return 0, false
		}
		w.mu.Lock()
		if len(w.queue) > 0 {
			id := w.queue[0]
			w.queue = w.queue[1:]
			delete(w.queued, id)
			w.mu.Unlock()
			return id, true
		}
		w.mu.Unlock()
		select {
		case <-w.wake:
		case <-ctx.Done():
			return 0, false
		}
	}
}

// process classifies one todo (step 3 spec 6.4): skip it if it is gone or no
// longer pending; fail it if there is no key; otherwise wait for the turn, ask
// the AI, and apply the answer or fail the todo. Every step is logged.
func (w *Worker) process(ctx context.Context, id int64) {
	todo, ok := w.pending(ctx, id)
	if !ok {
		return
	}
	if w.cfg.Classifier == nil {
		w.fail(ctx, todo, failure{
			short: "API anahtarı yok",
			details: "Hata türü: API anahtarı yok\n" +
				"GEMINI_API_KEY tanımlı değil. Anahtarla yeniden başlatıp “Tekrar dene” ile yeniden deneyin.",
		})
		return
	}
	categories, err := w.store.ListCategories(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("classify: list categories", "error", err)
		}
		return
	}

	if !w.waitForTurn(ctx) {
		return
	}
	// The user may have moved the todo, or it may be gone, while the worker waited.
	if todo, ok = w.pending(ctx, id); !ok {
		return
	}

	input := ai.Input{Title: todo.Title, Description: todo.Description, Categories: offered(categories)}
	w.log(applog.LevelInfo, "Sınıflandırılıyor: "+applog.Quote(todo.Title), "")

	callCtx := ctx
	if w.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, w.cfg.Timeout)
		defer cancel()
	}
	w.lastCall = time.Now()
	result, err := w.cfg.Classifier.Classify(callCtx, input)
	if ctx.Err() != nil {
		return // the server is stopping, which is no failure of the todo
	}
	if err != nil {
		w.fail(ctx, todo, w.describe(err))
		return
	}
	w.apply(ctx, todo, input, result, categories)
}

// pending returns the todo with that id if it exists and still waits for the AI.
// A todo that was deleted, moved by hand or dealt with already is not for the
// worker.
func (w *Worker) pending(ctx context.Context, id int64) (db.Todo, bool) {
	todo, err := w.store.GetTodo(ctx, id)
	if err != nil {
		if !errors.Is(err, db.ErrNotFound) && ctx.Err() == nil {
			slog.Error("classify: read todo", "id", id, "error", err)
		}
		return db.Todo{}, false
	}
	return todo, todo.Classification == db.ClassificationPending
}

// waitForTurn waits until MinInterval has passed since the start of the latest
// call. It returns false if ctx ended first.
func (w *Worker) waitForTurn(ctx context.Context) bool {
	if w.lastCall.IsZero() || w.cfg.MinInterval <= 0 {
		return ctx.Err() == nil
	}
	wait := w.cfg.MinInterval - time.Since(w.lastCall)
	if wait <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// The two shapes of metadata.ai (step 3 spec 4).
type (
	successMetadata struct {
		Model      string `json:"model"`
		Category   string `json:"category"`
		Reason     string `json:"reason"`
		DurationMS int64  `json:"duration_ms"`
		At         string `json:"at"`
	}
	failureMetadata struct {
		Model string `json:"model"`
		Error string `json:"error"`
		At    string `json:"at"`
	}
)

// apply records the answer of the AI. The store puts the todo in its category
// only if it is still in the inbox and still pending; if the user moved it by
// hand while the AI was thinking, the answer only goes to the log.
func (w *Worker) apply(ctx context.Context, todo db.Todo, in ai.Input, res ai.Result, categories []db.Category) {
	model := cmp.Or(res.Model, w.cfg.Model)
	reason := w.redact(res.Reason)
	metadata, _ := json.Marshal(successMetadata{
		Model: model, Category: res.Key, Reason: reason, DurationMS: res.Duration.Milliseconds(), At: now(),
	})

	_, applied, err := w.store.ApplyClassification(ctx, todo.ID, res.Key, metadata)
	name := categoryName(categories, res.Key)
	switch {
	case errors.Is(err, db.ErrNotFound):
		// Deleted while the AI was thinking; there is nothing to put anywhere.
	case errors.Is(err, db.ErrInvalidCategory):
		w.fail(ctx, todo, failure{
			short:   "geçersiz yanıt",
			details: fmt.Sprintf("Hata türü: geçersiz yanıt\nMesaj: %q kategorisi artık yok", res.Key),
		})
	case err != nil:
		if ctx.Err() != nil {
			return
		}
		slog.Error("classify: apply classification", "id", todo.ID, "error", err)
		w.fail(ctx, todo, failure{short: "veritabanı hatası", details: "Hata türü: veritabanı hatası\nMesaj: " + err.Error()})
	case !applied:
		w.log(applog.LevelWarn, "Öneri uygulanmadı: "+applog.Quote(todo.Title)+" (kart elle taşınmıştı)",
			"Önerilen kategori: "+name+" ("+res.Key+")\nGerekçe: "+reason)
	default:
		w.log(applog.LevelInfo, "Sınıflandırıldı: "+applog.Quote(todo.Title)+" → "+name+" ("+seconds(res.Duration)+" sn)",
			"Model: "+model+"\n"+
				"Giden metin:\n"+ai.BuildUserText(in)+"\n"+
				"Modelin cevabı (JSON): "+res.Raw+"\n"+
				"Gerekçe: "+reason+"\n"+
				"Süre: "+seconds(res.Duration)+" sn")
	}
}

// failure says why a todo could not be classified: short goes to metadata.ai.error,
// details to the log entry.
type failure struct {
	short   string
	details string
}

// fail marks the todo as failed, if it is still pending, and logs why. A todo
// that the user moved by hand while the AI was working is left as it is; the
// error is logged all the same, with a note.
func (w *Worker) fail(ctx context.Context, todo db.Todo, f failure) {
	metadata, _ := json.Marshal(failureMetadata{Model: w.cfg.Model, Error: w.redact(f.short), At: now()})
	details := f.details

	_, applied, err := w.store.FailClassification(ctx, todo.ID, metadata)
	switch {
	case errors.Is(err, db.ErrNotFound):
		details += "\nKart silinmişti."
	case err != nil:
		if ctx.Err() != nil {
			return
		}
		slog.Error("classify: fail classification", "id", todo.ID, "error", err)
		details += "\nKart güncellenemedi: " + err.Error()
	case !applied:
		details += "\nKart elle taşınmıştı; kartta değişiklik yapılmadı."
	}
	w.log(applog.LevelError, "Sınıflandırılamadı: "+applog.Quote(todo.Title), details)
}

// describe tells the errors of a Classifier apart (step 3 spec 6.1 and 8): the
// kind, the HTTP status and Retry-After if there are any, and the message.
func (w *Worker) describe(err error) failure {
	var quota *ai.QuotaError
	var httpErr *ai.HTTPError
	switch {
	case errors.As(err, &quota):
		lines := []string{"Hata türü: kota aşıldı", "HTTP durumu: 429"}
		if quota.RetryAfter != "" {
			lines = append(lines, "Retry-After: "+quota.RetryAfter)
		}
		lines = append(lines, "Model: "+w.cfg.Model, "Mesaj: "+err.Error())
		if quota.Body != "" {
			lines = append(lines, "Yanıt: "+quota.Body)
		}
		return failure{short: "kota aşıldı (HTTP 429)", details: strings.Join(lines, "\n")}

	case errors.As(err, &httpErr):
		status := strconv.Itoa(httpErr.Status)
		lines := []string{"Hata türü: HTTP hatası", "HTTP durumu: " + status, "Model: " + w.cfg.Model, "Mesaj: " + err.Error()}
		if httpErr.Body != "" {
			lines = append(lines, "Yanıt: "+httpErr.Body)
		}
		return failure{short: "HTTP hatası (" + status + ")", details: strings.Join(lines, "\n")}

	case errors.Is(err, ai.ErrBadResponse):
		return failure{
			short:   "geçersiz yanıt",
			details: "Hata türü: geçersiz yanıt\nModel: " + w.cfg.Model + "\nMesaj: " + err.Error(),
		}

	case errors.Is(err, context.DeadlineExceeded):
		short, details := "zaman aşımı", "Hata türü: zaman aşımı\n"
		if w.cfg.Timeout > 0 {
			short += " (" + w.cfg.Timeout.String() + ")"
			details += "Süre sınırı: " + w.cfg.Timeout.String() + "\n"
		}
		return failure{short: short, details: details + "Model: " + w.cfg.Model + "\nMesaj: " + err.Error()}

	default:
		return failure{
			short:   oneLine(err.Error(), 300),
			details: "Hata türü: bağlantı ya da bilinmeyen hata\nModel: " + w.cfg.Model + "\nMesaj: " + err.Error(),
		}
	}
}

// log adds an entry from the AI, with the API key blanked.
func (w *Worker) log(level, message, details string) {
	w.logs.Add(level, applog.SourceAI, w.redact(message), w.redact(details))
}

// redact blanks the API key in s.
func (w *Worker) redact(s string) string {
	if w.cfg.APIKey == "" {
		return s
	}
	return strings.ReplaceAll(s, w.cfg.APIKey, redacted)
}

// offered returns the categories the AI may choose from: all of them but the
// inbox, in column order.
func offered(categories []db.Category) []ai.Category {
	var out []ai.Category
	for _, c := range categories {
		if c.Key != db.InboxKey {
			out = append(out, ai.Category{Key: c.Key, Name: c.Name, Description: c.Description})
		}
	}
	return out
}

// categoryName returns the name of the category with that key, or the key if
// there is none.
func categoryName(categories []db.Category, key string) string {
	for _, c := range categories {
		if c.Key == key {
			return c.Name
		}
	}
	return key
}

// seconds formats a duration as seconds with one decimal and the Turkish
// comma: 0,4.
func seconds(d time.Duration) string {
	return strings.Replace(strconv.FormatFloat(d.Seconds(), 'f', 1, 64), ".", ",", 1)
}

// now returns the time for metadata.ai.at: UTC, to the millisecond, like the
// timestamps of the database.
func now() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// oneLine puts s on one line and cuts it after limit bytes (not in the middle
// of a character).
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
