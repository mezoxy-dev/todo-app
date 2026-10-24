// Command server serves the todo API.
//
// It is configured with two environment variables: ADDR, the address to listen
// on (default 127.0.0.1:8080), and DB_PATH, the SQLite database file (default
// data/todos.db, relative to the working directory).
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

	"todoapp/internal/api"
	"todoapp/internal/db"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

// run serves the API until it receives an interrupt or SIGTERM, then shuts the
// server down and closes the database.
func run() (err error) {
	addr := envOr("ADDR", "127.0.0.1:8080")
	dbPath := envOr("DB_PATH", "data/todos.db")

	store, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		// This runs after Shutdown has returned, so no request uses the store any more.
		if closeErr := store.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close database: %w", closeErr))
		}
	}()

	// Listening before logging, so that "listening" is not logged for an address
	// that turns out to be taken.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           api.New(store),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		// http.ErrServerClosed is the normal result of Shutdown, not a failure.
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	slog.Info("listening", "addr", "http://"+addr, "db", dbPath)

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
