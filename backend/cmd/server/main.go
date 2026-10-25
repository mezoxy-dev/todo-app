// Command server serves the todo API and runs the AI worker that puts new todos
// in their categories.
//
// It is configured with environment variables:
//
//   - ADDR: the address to listen on (default 127.0.0.1:8080).
//   - DB_PATH: the SQLite database file (default data/todos.db, relative to the
//     working directory).
//   - GEMINI_API_KEY: the key of the Gemini API. Without it the server works,
//     but the AI is off: new todos fail to classify with "API anahtarı yok".
//   - AI_MODEL: the Gemini model (default gemini-3.5-flash-lite).
//   - AI_TIMEOUT: the longest one call to the AI may take (default 20s).
//   - AI_MIN_INTERVAL: the shortest time between two calls (default 4s).
//   - GEMINI_BASE_URL: where the Gemini API is (default
//     https://generativelanguage.googleapis.com); a fake server in tests.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"todoapp/internal/ai"
	"todoapp/internal/api"
	"todoapp/internal/applog"
	"todoapp/internal/classify"
	"todoapp/internal/db"
)

// logCapacity is the number of entries the log panel can go back.
const logCapacity = 500

func main() {
	if err := run(); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

// run serves the API until it receives an interrupt or SIGTERM, then shuts the
// HTTP server down, stops the worker and closes the database, in that order.
func run() (err error) {
	addr := envOr("ADDR", "127.0.0.1:8080")
	dbPath := envOr("DB_PATH", "data/todos.db")
	settings, err := ai.SettingsFromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("read the settings of the AI: %w", err)
	}

	store, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		// This runs after the worker has stopped and Shutdown has returned, so
		// nothing uses the store any more.
		if closeErr := store.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close database: %w", closeErr))
		}
	}()

	logs := applog.New(logCapacity)
	// Without a key the classifier stays nil, which the worker takes to mean that
	// the AI is off. (A nil pointer in the interface would not be nil.)
	var classifier ai.Classifier
	if settings.APIKey == "" {
		logs.Add(applog.LevelWarn, applog.SourceSystem, "Yapay zekâ kapalı: GEMINI_API_KEY tanımlı değil", "")
	} else {
		classifier = ai.NewGemini(ai.GeminiConfig{
			APIKey:  settings.APIKey,
			Model:   settings.Model,
			BaseURL: settings.BaseURL,
			Timeout: settings.Timeout,
		})
	}
	worker := classify.NewWorker(store, logs, classify.Config{
		Classifier:  classifier,
		APIKey:      settings.APIKey,
		Model:       settings.Model,
		Timeout:     settings.Timeout,
		MinInterval: settings.MinInterval,
	})

	// Listening before logging, so that "listening" is not logged for an address
	// that turns out to be taken.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           api.New(store, logs, worker),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logs.Add(applog.LevelInfo, applog.SourceSystem, "Sunucu başladı (model: "+settings.Model+")", "")

	// The worker runs on the context of the server. It is stopped, and waited
	// for, when run returns: after the HTTP server has shut down, and before the
	// deferred Close of the database.
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.Run(workerCtx)
	}()
	defer func() {
		stopWorker()
		<-workerDone
	}()

	serveErr := make(chan error, 1)
	go func() {
		// http.ErrServerClosed is the normal result of Shutdown, not a failure.
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	slog.Info("listening", "addr", "http://"+addr, "db", dbPath, "model", settings.Model, "ai", settings.APIKey != "")

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// envOr returns the value of the environment variable key, or def if it is
// not set or empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
