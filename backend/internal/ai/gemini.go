package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// The request body follows the generateContent reference
// (https://ai.google.dev/api/generate-content), the REST discovery document of
// the Gemini API (https://generativelanguage.googleapis.com/$discovery/rest?version=v1beta)
// and the guides of the "Generate Content API (Legacy)" section, which were
// checked on 2026-10-25. The main guides (structured-output, thinking) now
// describe the Interactions endpoint, which this program does not use; for
// generateContent the names are these:
//
//   - systemInstruction is a Content: {"parts": [{"text": ...}]};
//   - contents is a list of Content with a role and parts;
//   - generationConfig.temperature is a float. The reference says that models from
//     Gemini 3.6 Flash on ignore it; earlier ones, such as the default model, use it;
//   - the answer is asked for as JSON with generationConfig.responseFormat.text,
//     {"mimeType": "APPLICATION_JSON", "schema": <JSON Schema>}, the form of the
//     REST examples in
//     https://ai.google.dev/gemini-api/docs/generate-content/structured-output.
//     The older responseMimeType with responseSchema or responseJsonSchema is
//     marked deprecated in the reference ("Use response_format instead"), so it is
//     not used. The schema is plain JSON Schema: lower case types, enum, required.
//
// No thinkingConfig is sent. The thinking guide of that section
// (https://ai.google.dev/gemini-api/docs/generate-content/thinking) lists
// "minimal" as the default level of Gemini 3.5 Flash-Lite, the lowest it has, so
// there is nothing to ask for. Sending a level would make AI_MODEL fail for the
// models it does not suit: Gemini 2.5 models do not take thinkingLevel (the
// reference: "Use with earlier models results in an error"), the guide marks
// "minimal" as an error for Gemini 4 Argon, and the Pro models do not offer it.
type (
	generateRequest struct {
		SystemInstruction content          `json:"systemInstruction"`
		Contents          []content        `json:"contents"`
		GenerationConfig  generationConfig `json:"generationConfig"`
	}
	content struct {
		Role  string `json:"role,omitempty"`
		Parts []part `json:"parts"`
	}
	part struct {
		Text string `json:"text"`
	}
	generationConfig struct {
		Temperature    float64        `json:"temperature"` // 0 is a value, not a missing one: no omitempty
		ResponseFormat responseFormat `json:"responseFormat"`
	}
	responseFormat struct {
		Text textFormat `json:"text"`
	}
	textFormat struct {
		MimeType string       `json:"mimeType"`
		Schema   answerSchema `json:"schema"`
	}
	answerSchema struct {
		Type       string                    `json:"type"`
		Properties map[string]schemaProperty `json:"properties"`
		Required   []string                  `json:"required"`
	}
	schemaProperty struct {
		Type string   `json:"type"`
		Enum []string `json:"enum,omitempty"`
	}
)

// generateResponse is the part of the answer that is read: the text of the
// first candidate, why it stopped, and why a prompt was blocked.
type generateResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text    string `json:"text"`
				Thought bool   `json:"thought"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

const (
	maxResponseBytes = 1 << 20 // a classification answer is tiny; this is a guard
	bodyLimit        = 2000    // bytes of an answer that an error keeps
	excerptLimit     = 200     // bytes of the model's text that a message about it keeps
	redacted         = "***"   // what stands where the API key was
)

// GeminiConfig configures a Gemini classifier.
type GeminiConfig struct {
	APIKey  string        // sent in the x-goog-api-key header and nowhere else
	Model   string        // e.g. "gemini-3.5-flash-lite"
	BaseURL string        // e.g. "https://generativelanguage.googleapis.com"; a fake server in tests
	Timeout time.Duration // the longest a call may take; zero: only the context of the caller limits it
}

// Gemini is a Classifier that asks the Gemini API. It is safe for concurrent use.
type Gemini struct {
	cfg    GeminiConfig
	client *http.Client
	url    string
}

// NewGemini returns a classifier for the model and the key of cfg.
func NewGemini(cfg GeminiConfig) *Gemini {
	return &Gemini{
		cfg: cfg,
		client: &http.Client{
			// The key is sent to BaseURL and nowhere else: a redirect to another
			// host would carry the header along. A redirect is simply the answer.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		url: strings.TrimRight(cfg.BaseURL, "/") + "/v1beta/models/" + url.PathEscape(cfg.Model) + ":generateContent",
	}
}

// Classify asks the model for the category of the todo. The call takes at most
// Timeout. See Classifier for the errors; none of them contains the API key.
func (g *Gemini) Classify(ctx context.Context, in Input) (Result, error) {
	if g.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.cfg.Timeout)
		defer cancel()
	}

	body, err := json.Marshal(newRequest(in))
	if err != nil {
		return Result{}, fmt.Errorf("encode gemini request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("build gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.cfg.APIKey)

	// net/http leaves header values out of its errors, and the URL has no key.
	start := time.Now()
	resp, err := g.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("call gemini: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	duration := time.Since(start)
	if err != nil {
		return Result{}, fmt.Errorf("read gemini response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return Result{}, &QuotaError{RetryAfter: resp.Header.Get("Retry-After"), Body: g.excerpt(string(data), bodyLimit)}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return Result{}, &HTTPError{Status: resp.StatusCode, Body: g.excerpt(string(data), bodyLimit)}
	}

	key, reason, raw, err := g.parse(data, in.Categories)
	if err != nil {
		return Result{}, err
	}
	return Result{Key: key, Reason: reason, Model: g.cfg.Model, Raw: raw, Duration: duration}, nil
}

// newRequest builds the request for a todo: the instruction from the
// categories, the todo as the user's message, temperature 0 so that the same
// todo gets the same answer, and a schema that lets the model answer with one of
// the keys only.
func newRequest(in Input) generateRequest {
	keys := make([]string, len(in.Categories))
	for i, c := range in.Categories {
		keys[i] = c.Key
	}
	return generateRequest{
		SystemInstruction: content{Parts: []part{{Text: BuildSystemPrompt(in.Categories)}}},
		Contents:          []content{{Role: "user", Parts: []part{{Text: BuildUserText(in)}}}},
		GenerationConfig: generationConfig{
			Temperature: 0,
			ResponseFormat: responseFormat{Text: textFormat{
				MimeType: "APPLICATION_JSON",
				Schema: answerSchema{
					Type: "object",
					Properties: map[string]schemaProperty{
						"category": {Type: "string", Enum: keys},
						"reason":   {Type: "string"},
					},
					Required: []string{"category", "reason"},
				},
			}},
		},
	}
}

// parse reads the answer of a successful call: the text of the first candidate
// must be the JSON object {"category", "reason"} with a category from cats. raw
// is that text. Anything else is ErrBadResponse, with the reason.
func (g *Gemini) parse(data []byte, cats []Category) (key, reason, raw string, err error) {
	var resp generateResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", "", "", g.bad("the body is not JSON (%v): %s", err, g.excerpt(string(data), excerptLimit))
	}
	if len(resp.Candidates) == 0 {
		if why := resp.PromptFeedback.BlockReason; why != "" {
			return "", "", "", g.bad("the prompt was blocked (%s)", why)
		}
		return "", "", "", g.bad("the response has no candidate")
	}

	candidate := resp.Candidates[0]
	finish := "no finish reason"
	if candidate.FinishReason != "" {
		finish = "finish reason " + candidate.FinishReason
	}
	// The model's thoughts, if it sent any, are not part of the answer.
	var text strings.Builder
	for _, p := range candidate.Content.Parts {
		if !p.Thought {
			text.WriteString(p.Text)
		}
	}
	raw = strings.TrimSpace(text.String())
	if raw == "" {
		return "", "", "", g.bad("the candidate has no text (%s)", finish)
	}

	var answer struct {
		Category string `json:"category"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &answer); err != nil {
		return "", "", "", g.bad("the text is not the JSON object that was asked for (%s): %s", finish, g.excerpt(raw, excerptLimit))
	}
	if !slices.ContainsFunc(cats, func(c Category) bool { return c.Key == answer.Category }) {
		return "", "", "", g.bad("category %q is not one of the %d that were offered", answer.Category, len(cats))
	}
	return answer.Category, strings.TrimSpace(answer.Reason), raw, nil
}

// bad returns ErrBadResponse with the reason, in which the API key, should the
// service have echoed it, is blanked.
func (g *Gemini) bad(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadResponse, g.redact(fmt.Sprintf(format, args...)))
}

// redact blanks the API key in s.
func (g *Gemini) redact(s string) string {
	if g.cfg.APIKey == "" {
		return s
	}
	return strings.ReplaceAll(s, g.cfg.APIKey, redacted)
}

// excerpt returns the start of a text from the service for an error: trimmed,
// without the API key, and cut after at most limit bytes (not in the middle of a
// character), with "…" to say so.
func (g *Gemini) excerpt(s string, limit int) string {
	s = strings.TrimSpace(g.redact(s))
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
