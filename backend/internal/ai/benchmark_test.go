package ai

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// benchmarkSample is one labelled todo of testdata/benchmark.json, the set that
// cmd/aibench measures the model with (step 3 spec 6.6).
type benchmarkSample struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Expected    string `json:"expected"`
}

// The set is data that nothing else reads while go test runs, so this keeps it
// honest: it must have the size and the spread the spec asks for.
func TestBenchmarkSetIsWellFormed(t *testing.T) {
	raw, err := os.ReadFile("testdata/benchmark.json")
	if err != nil {
		t.Fatalf("read the benchmark set: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // a misspelt "expected" must not pass as a todo without a label
	var samples []benchmarkSample
	if err := dec.Decode(&samples); err != nil {
		t.Fatalf("decode the benchmark set: %v", err)
	}

	if len(samples) < 40 {
		t.Errorf("%d samples, want about 40 or more", len(samples))
	}
	perCategory := map[string]int{}
	titles := map[string]bool{}
	var all strings.Builder
	for i, s := range samples {
		if strings.TrimSpace(s.Title) == "" || utf8.RuneCountInString(s.Title) > 500 {
			t.Errorf("sample %d: title %q, want 1 to 500 characters", i+1, s.Title)
		}
		if !utf8.ValidString(s.Title) || !utf8.ValidString(s.Description) {
			t.Errorf("sample %d (%q): not valid UTF-8", i+1, s.Title)
		}
		if s.Description != strings.TrimSpace(s.Description) || utf8.RuneCountInString(s.Description) > 2000 {
			t.Errorf("sample %d (%q): the description must be trimmed and at most 2000 characters", i+1, s.Title)
		}
		if !slices.ContainsFunc(testCategories, func(c Category) bool { return c.Key == s.Expected }) {
			t.Errorf("sample %d (%q): expected %q is not one of the categories the model is offered", i+1, s.Title, s.Expected)
		}
		if titles[strings.ToLower(s.Title)] {
			t.Errorf("sample %d: the title %q is there twice", i+1, s.Title)
		}
		titles[strings.ToLower(s.Title)] = true
		perCategory[s.Expected]++
		all.WriteString(s.Title + " " + s.Description + " ")
	}

	// At least four of each category, so that its row in the report means something.
	for _, c := range testCategories {
		if perCategory[c.Key] < 4 {
			t.Errorf("category %s has %d samples, want at least 4", c.Key, perCategory[c.Key])
		}
	}

	// The ambiguous todos that must go to research: too little to decide on.
	for _, title := range []string{"hediye", "tatil", "laptop karşılaştır"} {
		var found bool
		for _, s := range samples {
			if strings.HasPrefix(strings.ToLower(s.Title), title) {
				found = true
				if s.Expected != "research" {
					t.Errorf("sample %q is expected to go to %q, want research", s.Title, s.Expected)
				}
			}
		}
		if !found {
			t.Errorf("no sample starts with %q", title)
		}
	}

	// Turkish, with its letters.
	for _, letter := range []string{"ç", "ğ", "ı", "ö", "ş", "ü", "İ"} {
		if !strings.Contains(all.String(), letter) {
			t.Errorf("no sample has the letter %q", letter)
		}
	}
}
