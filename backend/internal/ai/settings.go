package ai

import (
	"fmt"
	"strings"
	"time"
)

// The defaults of the settings (step 3 spec 6.5).
const (
	DefaultModel       = "gemini-3.5-flash-lite"
	DefaultBaseURL     = "https://generativelanguage.googleapis.com"
	DefaultTimeout     = 20 * time.Second
	DefaultMinInterval = 4 * time.Second
)

// Settings are the AI settings that the server and cmd/aibench read from the
// environment.
type Settings struct {
	APIKey      string        // GEMINI_API_KEY; empty: there is no key, the AI is off
	Model       string        // AI_MODEL
	BaseURL     string        // GEMINI_BASE_URL: where the API is, a fake server in tests
	Timeout     time.Duration // AI_TIMEOUT: the longest a call may take
	MinInterval time.Duration // AI_MIN_INTERVAL: the shortest time between the starts of two calls
}

// SettingsFromEnv reads the settings with getenv (os.Getenv in a command). A
// variable that is not set or empty has its default, and white space around a
// value, which setx and copy and paste leave behind, is cut off. A duration
// that cannot be read, or a timeout that is not positive, is an error; the
// message names the variable and never contains the API key.
func SettingsFromEnv(getenv func(string) string) (Settings, error) {
	value := func(name, def string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return def
	}
	s := Settings{
		APIKey:  strings.TrimSpace(getenv("GEMINI_API_KEY")),
		Model:   value("AI_MODEL", DefaultModel),
		BaseURL: value("GEMINI_BASE_URL", DefaultBaseURL),
	}

	var err error
	if s.Timeout, err = parseDuration("AI_TIMEOUT", value("AI_TIMEOUT", ""), DefaultTimeout); err != nil {
		return Settings{}, err
	}
	if s.Timeout <= 0 {
		return Settings{}, fmt.Errorf("invalid AI_TIMEOUT %q: must be greater than zero", s.Timeout)
	}
	if s.MinInterval, err = parseDuration("AI_MIN_INTERVAL", value("AI_MIN_INTERVAL", ""), DefaultMinInterval); err != nil {
		return Settings{}, err
	}
	if s.MinInterval < 0 {
		return Settings{}, fmt.Errorf("invalid AI_MIN_INTERVAL %q: must not be negative", s.MinInterval)
	}
	return s, nil
}

// parseDuration reads the value of the variable name, or returns def if there is none.
func parseDuration(name, v string, def time.Duration) (time.Duration, error) {
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	return d, nil
}
