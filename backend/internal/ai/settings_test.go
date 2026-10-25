package ai

import (
	"strings"
	"testing"
	"time"
)

// env returns a getenv function that knows only the given variables.
func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// Step 3 spec 6.5.
func TestSettingsFromEnv(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got, err := SettingsFromEnv(env(nil))

		if err != nil {
			t.Fatalf("SettingsFromEnv: %v", err)
		}
		want := Settings{
			APIKey:      "",
			Model:       "gemini-3.5-flash-lite",
			BaseURL:     "https://generativelanguage.googleapis.com",
			Timeout:     20 * time.Second,
			MinInterval: 4 * time.Second,
		}
		if got != want {
			t.Errorf("Settings = %+v, want %+v", got, want)
		}
	})

	t.Run("every variable", func(t *testing.T) {
		got, err := SettingsFromEnv(env(map[string]string{
			"GEMINI_API_KEY":  "  secret-key\r\n", // setx and copy-paste leave white space behind
			"AI_MODEL":        "gemini-x",
			"AI_TIMEOUT":      "1m30s",
			"AI_MIN_INTERVAL": "250ms",
			"GEMINI_BASE_URL": "http://127.0.0.1:9999",
		}))

		if err != nil {
			t.Fatalf("SettingsFromEnv: %v", err)
		}
		want := Settings{
			APIKey:      "secret-key",
			Model:       "gemini-x",
			BaseURL:     "http://127.0.0.1:9999",
			Timeout:     90 * time.Second,
			MinInterval: 250 * time.Millisecond,
		}
		if got != want {
			t.Errorf("Settings = %+v, want %+v", got, want)
		}
	})

	t.Run("empty variables count as not set", func(t *testing.T) {
		got, err := SettingsFromEnv(env(map[string]string{"AI_MODEL": "", "AI_TIMEOUT": "", "AI_MIN_INTERVAL": "", "GEMINI_BASE_URL": ""}))

		if err != nil {
			t.Fatalf("SettingsFromEnv: %v", err)
		}
		if got.Model != DefaultModel || got.Timeout != DefaultTimeout || got.MinInterval != DefaultMinInterval || got.BaseURL != DefaultBaseURL {
			t.Errorf("Settings = %+v, want the defaults", got)
		}
	})

	t.Run("no pacing is allowed", func(t *testing.T) {
		got, err := SettingsFromEnv(env(map[string]string{"AI_MIN_INTERVAL": "0s"}))

		if err != nil || got.MinInterval != 0 {
			t.Errorf("AI_MIN_INTERVAL=0s: Settings %+v, err %v; want an interval of 0", got, err)
		}
	})

	t.Run("invalid durations", func(t *testing.T) {
		tests := []struct {
			name string
			vars map[string]string
			want string // what the message names
		}{
			{"no unit", map[string]string{"AI_TIMEOUT": "20"}, "AI_TIMEOUT"},
			{"not a duration", map[string]string{"AI_TIMEOUT": "çok"}, "AI_TIMEOUT"},
			{"zero timeout", map[string]string{"AI_TIMEOUT": "0s"}, "AI_TIMEOUT"},
			{"negative timeout", map[string]string{"AI_TIMEOUT": "-5s"}, "AI_TIMEOUT"},
			{"interval without unit", map[string]string{"AI_MIN_INTERVAL": "4"}, "AI_MIN_INTERVAL"},
			{"negative interval", map[string]string{"AI_MIN_INTERVAL": "-1s"}, "AI_MIN_INTERVAL"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				tc.vars["GEMINI_API_KEY"] = "secret-key"

				_, err := SettingsFromEnv(env(tc.vars))

				if err == nil {
					t.Fatal("SettingsFromEnv succeeded, want an error")
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("err = %q, want it to name %s", err, tc.want)
				}
				if strings.Contains(err.Error(), "secret-key") {
					t.Errorf("err = %q, contains the API key", err)
				}
			})
		}
	})
}
