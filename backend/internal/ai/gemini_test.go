package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testKey   = "test-key"
	testModel = "gemini-3.5-flash-lite"
)

// testCategories are the categories the AI chooses from: those of migration
// 0003 without the inbox, in column order.
var testCategories = []Category{
	{Key: "purchase", Name: "Satın alım", Description: "Satın alınacak ya da sipariş edilecek şeyler. Örnek: süt al, kulaklık sipariş et."},
	{Key: "research", Name: "Araştırma", Description: "Bilgi toplama, öğrenme, karşılaştırma ya da ne yapılacağı için gerekli bilgisi verilmemiş belirsiz işler. Örnek: tatil için otel bak, X nedir öğren, bir hediye bul."},
	{Key: "communication", Name: "İletişim", Description: "Birini arama, mesaj ya da e-posta gönderme. Örnek: annemi ara, Ali'ye mail at."},
	{Key: "payment", Name: "Ödeme", Description: "Fatura, kira, vergi ve diğer para işleri. Örnek: elektrik faturasını öde."},
	{Key: "appointment", Name: "Randevu & Etkinlik", Description: "Tarihi ya da saati olan işler. Örnek: dişçi randevusu al, cuma toplantısı."},
	{Key: "work", Name: "İş", Description: "Mesleki görevler. Örnek: sunumu bitir, kod incelemesi."},
	{Key: "home", Name: "Ev & Kişisel", Description: "Ev işleri, sağlık, spor, kişisel bakım. Örnek: çamaşır yıka, spora git."},
	{Key: "other", Name: "Diğer", Description: "Hiçbir kategoriye uymayanlar."},
}

var testKeys = []string{"purchase", "research", "communication", "payment", "appointment", "work", "home", "other"}

// reply is the body of a successful generateContent answer whose only text is
// text, the way the model answers a request for JSON.
func reply(t *testing.T, text string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}},
			"finishReason": "STOP",
		}},
		"modelVersion": testModel,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// newGemini starts a fake Gemini that answers every request with handler and
// returns a client for it. The server is closed when the test ends.
func newGemini(t *testing.T, timeout time.Duration, handler http.HandlerFunc) *Gemini {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewGemini(GeminiConfig{APIKey: testKey, Model: testModel, BaseURL: srv.URL, Timeout: timeout})
}

// answer returns a handler that sends status and body.
func answer(status int, body string, header ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

// hang is a handler that does not answer until the client gives up. It reads the
// request first, because the server only notices that the client has gone once
// the body has been read. The wait is long, but not for ever: closing the test
// server waits for the handlers.
func hang(w http.ResponseWriter, r *http.Request) {
	io.Copy(io.Discard, r.Body)
	select {
	case <-r.Context().Done():
	case <-time.After(5 * time.Second):
	}
}

func classify(g *Gemini) (Result, error) {
	return g.Classify(context.Background(), Input{Title: "Süt al", Categories: testCategories})
}

// Step 3 spec 6.2: the request. The field names are those of the generateContent
// reference and of the REST discovery document of the Gemini API (see gemini.go).
func TestGeminiRequestShape(t *testing.T) {
	var (
		method, path, rawQuery, contentType, apiKey string
		body                                        []byte
	)
	g := newGemini(t, 5*time.Second, func(w http.ResponseWriter, r *http.Request) {
		method, path, rawQuery = r.Method, r.URL.Path, r.URL.RawQuery
		contentType, apiKey = r.Header.Get("Content-Type"), r.Header.Get("x-goog-api-key")
		body, _ = io.ReadAll(r.Body)
		answer(http.StatusOK, reply(t, `{"category":"purchase","reason":"Market alışverişi."}`))(w, r)
	})

	in := Input{Title: "Süt al", Description: "2 litre", Categories: testCategories}
	if _, err := g.Classify(context.Background(), in); err != nil {
		t.Fatalf("Classify: %v", err)
	}

	if method != http.MethodPost {
		t.Errorf("method = %s, want POST", method)
	}
	if want := "/v1beta/models/gemini-3.5-flash-lite:generateContent"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if apiKey != testKey {
		t.Errorf("x-goog-api-key = %q, want %q", apiKey, testKey)
	}
	if rawQuery != "" || strings.Contains(rawQuery, "key=") {
		t.Errorf("query = %q, want none: the key belongs in the header only", rawQuery)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	if strings.Contains(string(body), testKey) {
		t.Errorf("the request body contains the API key: %s", body)
	}

	var req struct {
		SystemInstruction struct {
			Parts []struct{ Text string } `json:"parts"`
		} `json:"systemInstruction"`
		Contents []struct {
			Role  string                  `json:"role"`
			Parts []struct{ Text string } `json:"parts"`
		} `json:"contents"`
		GenerationConfig map[string]json.RawMessage `json:"generationConfig"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, body)
	}

	// The system instruction is the prompt built from the categories.
	if len(req.SystemInstruction.Parts) != 1 || req.SystemInstruction.Parts[0].Text != BuildSystemPrompt(testCategories) {
		t.Errorf("systemInstruction = %+v, want one part holding BuildSystemPrompt(categories)", req.SystemInstruction)
	}
	// The content is what the user wrote, nothing else.
	if len(req.Contents) != 1 || req.Contents[0].Role != "user" || len(req.Contents[0].Parts) != 1 {
		t.Fatalf("contents = %+v, want one user message of one part", req.Contents)
	}
	if got, want := req.Contents[0].Parts[0].Text, "Başlık: Süt al\nAçıklama: 2 litre"; got != want {
		t.Errorf("user text = %q, want %q", got, want)
	}

	// Generation: temperature 0 (present, not left out as a zero value), and the
	// answer as JSON in the shape of the schema.
	if got := string(req.GenerationConfig["temperature"]); got != "0" {
		t.Errorf("generationConfig.temperature = %q, want 0", got)
	}
	// The deprecated ways of asking for JSON are not used next to the current one.
	for _, deprecated := range []string{"responseMimeType", "responseSchema", "responseJsonSchema", "_responseJsonSchema"} {
		if _, ok := req.GenerationConfig[deprecated]; ok {
			t.Errorf("generationConfig.%s is set: the reference deprecates it in favor of responseFormat", deprecated)
		}
	}
	var format struct {
		Text struct {
			MimeType string `json:"mimeType"`
			Schema   struct {
				Type       string `json:"type"`
				Properties map[string]struct {
					Type string   `json:"type"`
					Enum []string `json:"enum"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"schema"`
		} `json:"text"`
	}
	if err := json.Unmarshal(req.GenerationConfig["responseFormat"], &format); err != nil {
		t.Fatalf("generationConfig.responseFormat: %v (generationConfig: %s)", err, body)
	}
	if format.Text.MimeType != "APPLICATION_JSON" {
		t.Errorf("responseFormat.text.mimeType = %q, want APPLICATION_JSON", format.Text.MimeType)
	}
	schema := format.Text.Schema
	if schema.Type != "object" {
		t.Errorf("schema type = %q, want object", schema.Type)
	}
	category := schema.Properties["category"]
	if category.Type != "string" || !slices.Equal(category.Enum, testKeys) {
		t.Errorf("schema category = %+v, want a string enum of the 8 keys %v", category, testKeys)
	}
	if slices.Contains(category.Enum, "inbox") {
		t.Error("the category enum offers the inbox")
	}
	if reason := schema.Properties["reason"]; reason.Type != "string" {
		t.Errorf("schema reason = %+v, want a string", reason)
	}
	if len(schema.Properties) != 2 {
		t.Errorf("schema has the properties %v, want category and reason only", schema.Properties)
	}
	if required := slices.Sorted(slices.Values(schema.Required)); !slices.Equal(required, []string{"category", "reason"}) {
		t.Errorf("schema required = %v, want category and reason", schema.Required)
	}
}

func TestGeminiRequestWithoutDescriptionOrWithSlashInBaseURL(t *testing.T) {
	var path, userText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		var req struct {
			Contents []struct {
				Parts []struct{ Text string } `json:"parts"`
			} `json:"contents"`
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)
		userText = req.Contents[0].Parts[0].Text
		answer(http.StatusOK, reply(t, `{"category":"home","reason":"Ev işi."}`))(w, r)
	}))
	t.Cleanup(srv.Close)
	g := NewGemini(GeminiConfig{APIKey: testKey, Model: "m", BaseURL: srv.URL + "/", Timeout: time.Second})

	if _, err := g.Classify(context.Background(), Input{Title: "Çamaşır yıka", Categories: testCategories}); err != nil {
		t.Fatalf("Classify: %v", err)
	}

	// With no description there is no "Açıklama" line.
	if userText != "Başlık: Çamaşır yıka" {
		t.Errorf("user text = %q, want only the title line", userText)
	}
	// A slash at the end of the base URL does not double up in the path.
	if path != "/v1beta/models/m:generateContent" {
		t.Errorf("path = %q, want /v1beta/models/m:generateContent", path)
	}
}

func TestGeminiParsesResult(t *testing.T) {
	const text = `{"category":"purchase","reason":"Market alışverişi."}`
	g := newGemini(t, 5*time.Second, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond) // a real call takes time; the clock may be coarse
		answer(http.StatusOK, reply(t, text))(w, r)
	})

	got, err := classify(g)

	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got.Key != "purchase" || got.Reason != "Market alışverişi." {
		t.Errorf("Key %q, Reason %q; want purchase and %q", got.Key, got.Reason, "Market alışverişi.")
	}
	if got.Model != testModel {
		t.Errorf("Model = %q, want %q", got.Model, testModel)
	}
	if got.Raw != text {
		t.Errorf("Raw = %q, want the JSON text the model sent: %q", got.Raw, text)
	}
	if got.Duration < 10*time.Millisecond || got.Duration > 3*time.Second {
		t.Errorf("Duration = %v, want about the time of the call (at least 10ms)", got.Duration)
	}
}

// The text of the answer is the parts that are not thoughts, put together.
func TestGeminiAnswerParts(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[
		{"text":"düşünüyorum...","thought":true},
		{"text":"{\"category\":\"work\","},
		{"text":"\"reason\":\"Mesleki görev.\"}\n"}
	]},"finishReason":"STOP"}]}`
	g := newGemini(t, 5*time.Second, answer(http.StatusOK, body))

	got, err := classify(g)

	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got.Key != "work" || got.Reason != "Mesleki görev." {
		t.Errorf("Key %q, Reason %q; want work and %q", got.Key, got.Reason, "Mesleki görev.")
	}
	if got.Raw != `{"category":"work","reason":"Mesleki görev."}` {
		t.Errorf("Raw = %q, want the parts that are not thoughts, trimmed", got.Raw)
	}
}

func TestGeminiErrors(t *testing.T) {
	// echo is a body that repeats the key, the way an error page can; no error
	// may pass it on.
	echo := `{"error":{"code":400,"message":"API key not valid: ` + testKey + `. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`

	t.Run("quota", func(t *testing.T) {
		g := newGemini(t, 5*time.Second, answer(http.StatusTooManyRequests,
			`{"error":{"code":429,"message":"You exceeded your current quota (`+testKey+`)","status":"RESOURCE_EXHAUSTED"}}`,
			"Retry-After", "30"))

		_, err := classify(g)

		var quota *QuotaError
		if !errors.As(err, &quota) {
			t.Fatalf("err = %v, want a *QuotaError", err)
		}
		if quota.RetryAfter != "30" {
			t.Errorf("RetryAfter = %q, want 30", quota.RetryAfter)
		}
		if !strings.Contains(quota.Body, "RESOURCE_EXHAUSTED") {
			t.Errorf("Body = %q, want the start of the answer of the server", quota.Body)
		}
		wantNoKey(t, err, quota.Body)
		if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "30") {
			t.Errorf("err = %q, want it to name the status 429 and the delay", err)
		}
	})

	t.Run("quota without Retry-After", func(t *testing.T) {
		g := newGemini(t, 5*time.Second, answer(http.StatusTooManyRequests, `{}`))

		_, err := classify(g)

		var quota *QuotaError
		if !errors.As(err, &quota) || quota.RetryAfter != "" {
			t.Errorf("err = %v, want a *QuotaError without a delay", err)
		}
	})

	t.Run("HTTP errors", func(t *testing.T) {
		for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable} {
			g := newGemini(t, 5*time.Second, answer(status, echo))

			_, err := classify(g)

			var httpErr *HTTPError
			if !errors.As(err, &httpErr) {
				t.Errorf("status %d: err = %v, want an *HTTPError", status, err)
				continue
			}
			if httpErr.Status != status {
				t.Errorf("status %d: HTTPError.Status = %d", status, httpErr.Status)
			}
			if !strings.Contains(httpErr.Body, "INVALID_ARGUMENT") {
				t.Errorf("status %d: Body = %q, want the answer of the server", status, httpErr.Body)
			}
			wantNoKey(t, err, httpErr.Body)
			var quota *QuotaError
			if errors.As(err, &quota) || errors.Is(err, ErrBadResponse) {
				t.Errorf("status %d: err = %v, want nothing but an HTTPError", status, err)
			}
		}
	})

	t.Run("a long body is cut", func(t *testing.T) {
		g := newGemini(t, 5*time.Second, answer(http.StatusInternalServerError, strings.Repeat("ğ", 10000)))

		_, err := classify(g)

		var httpErr *HTTPError
		if !errors.As(err, &httpErr) {
			t.Fatalf("err = %v, want an *HTTPError", err)
		}
		if len(httpErr.Body) == 0 || len(httpErr.Body) > 2100 {
			t.Errorf("len(Body) = %d bytes, want it cut to about 2000", len(httpErr.Body))
		}
		if !strings.HasSuffix(httpErr.Body, "…") {
			t.Errorf("Body ends with %q, want it to say that it was cut", httpErr.Body[max(0, len(httpErr.Body)-8):])
		}
		for _, r := range httpErr.Body {
			if r == '�' {
				t.Fatal("Body was cut in the middle of a character")
			}
		}
	})

	t.Run("invalid answers", func(t *testing.T) {
		tests := []struct {
			name, body string
		}{
			{"text that is not JSON", reply(t, "Merhaba! Size nasıl yardımcı olabilirim?")},
			{"JSON that is not an object", reply(t, `"purchase"`)},
			{"an object without a category", reply(t, `{"reason":"x"}`)},
			{"null", reply(t, `null`)},
			{"an empty category", reply(t, `{"category":"","reason":"x"}`)},
			{"the inbox", reply(t, `{"category":"inbox","reason":"x"}`)},
			{"a category that does not exist", reply(t, `{"category":"xyz","reason":"x"}`)},
			{"a different case", reply(t, `{"category":"Purchase","reason":"x"}`)},
			{"the name instead of the key", reply(t, `{"category":"Satın alım","reason":"x"}`)},
			{"a category that is not even text", reply(t, `{"category":7,"reason":"x"}`)},
			{"an answer that echoes the key", reply(t, `{"category":"`+testKey+`","reason":"x"}`)},
			{"no candidates", `{"candidates":[]}`},
			{"an empty answer", `{}`},
			{"a candidate without text", `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}]}`},
			{"a blocked prompt", `{"promptFeedback":{"blockReason":"SAFETY"}}`},
			{"cut off by the token limit", `{"candidates":[{"content":{"parts":[{"text":"{\"category\":\"purch"}]},"finishReason":"MAX_TOKENS"}]}`},
			{"a body that is not JSON", `<html>502 Bad Gateway</html>`},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				g := newGemini(t, 5*time.Second, answer(http.StatusOK, tc.body))

				got, err := classify(g)

				if !errors.Is(err, ErrBadResponse) {
					t.Fatalf("err = %v, want ErrBadResponse (result %+v)", err, got)
				}
				wantNoKey(t, err)
				var httpErr *HTTPError
				var quota *QuotaError
				if errors.As(err, &httpErr) || errors.As(err, &quota) {
					t.Errorf("err = %v, want ErrBadResponse only", err)
				}
			})
		}
	})

	t.Run("the reason names why the answer was refused", func(t *testing.T) {
		blocked := newGemini(t, 5*time.Second, answer(http.StatusOK, `{"promptFeedback":{"blockReason":"SAFETY"}}`))
		if _, err := classify(blocked); err == nil || !strings.Contains(err.Error(), "SAFETY") {
			t.Errorf("blocked prompt: err = %v, want it to name SAFETY", err)
		}
		cut := newGemini(t, 5*time.Second, answer(http.StatusOK,
			`{"candidates":[{"content":{"parts":[{"text":"{\"cat"}]},"finishReason":"MAX_TOKENS"}]}`))
		if _, err := classify(cut); err == nil || !strings.Contains(err.Error(), "MAX_TOKENS") {
			t.Errorf("cut answer: err = %v, want it to name MAX_TOKENS", err)
		}
		unknown := newGemini(t, 5*time.Second, answer(http.StatusOK, reply(t, `{"category":"xyz","reason":"x"}`)))
		if _, err := classify(unknown); err == nil || !strings.Contains(err.Error(), `"xyz"`) {
			t.Errorf("unknown category: err = %v, want it to name the category", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		g := newGemini(t, 50*time.Millisecond, hang)

		start := time.Now()
		_, err := classify(g)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want it to be context.DeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("Classify took %v with a timeout of 50ms", elapsed)
		}
		wantNoKey(t, err)
		var httpErr *HTTPError
		if errors.As(err, &httpErr) || errors.Is(err, ErrBadResponse) {
			t.Errorf("err = %v, want only the deadline", err)
		}
	})

	t.Run("a context that is cancelled", func(t *testing.T) {
		g := newGemini(t, 5*time.Second, hang)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)

		_, err := g.Classify(ctx, Input{Title: "x", Categories: testCategories})

		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})

	t.Run("a server that is not there", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		g := NewGemini(GeminiConfig{APIKey: testKey, Model: testModel, BaseURL: url, Timeout: time.Second})

		_, err := classify(g)

		if err == nil {
			t.Fatal("Classify succeeded against a closed server")
		}
		wantNoKey(t, err)
		var httpErr *HTTPError
		if errors.As(err, &httpErr) || errors.Is(err, ErrBadResponse) || errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want a plain connection error", err)
		}
	})
}

// The key goes to the base URL only. A redirect would carry the header to
// another host, so it is not followed: it is the answer, and an error.
func TestGeminiDoesNotFollowRedirects(t *testing.T) {
	var reached atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
	}))
	t.Cleanup(elsewhere.Close)
	for _, status := range []int{http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		g := newGemini(t, 5*time.Second, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+"/v1beta/models/m:generateContent", status)
		})

		_, err := classify(g)

		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != status {
			t.Errorf("redirect %d: err = %v, want an *HTTPError with that status", status, err)
		}
		if reached.Load() {
			t.Fatalf("redirect %d was followed: the API key would have been sent to another host", status)
		}
	}
}

// wantNoKey fails the test if the key shows in any of the texts or in the text
// of err.
func wantNoKey(t *testing.T, err error, texts ...string) {
	t.Helper()
	if err != nil {
		texts = append(texts, err.Error())
	}
	for _, text := range texts {
		if strings.Contains(text, testKey) {
			t.Errorf("the API key shows in %q", text)
		}
	}
}

// Step 3 spec 6.3, character for character, with the categories filled in.
func TestBuildSystemPrompt(t *testing.T) {
	const want = `Sen bir yapılacaklar listesi asistanısın. Kullanıcının yazdığı görevi aşağıdaki kategorilerden
TAM OLARAK BİRİNE yerleştir.
Kurallar:
1. Görev bir şeyi öğrenmeyi, araştırmayı ya da karşılaştırmayı gerektiriyorsa veya ne yapılacağına
   karar vermek için gerekli bilgi verilmemişse "research" seç.
2. Hiçbir kategori uymuyorsa "other" seç.
3. "reason" alanına seçimini açıklayan tek kısa Türkçe cümle yaz.
Kategoriler:
- purchase (Satın alım): Satın alınacak ya da sipariş edilecek şeyler. Örnek: süt al, kulaklık sipariş et.
- communication (İletişim): Birini arama, mesaj ya da e-posta gönderme. Örnek: annemi ara, Ali'ye mail at.
- other (Diğer): Hiçbir kategoriye uymayanlar.`
	// The order is that of the slice, which is the column order, not alphabetical.
	cats := []Category{testCategories[0], testCategories[2], testCategories[7]}

	got := BuildSystemPrompt(cats)

	if got != want {
		t.Errorf("BuildSystemPrompt =\n%s\n\nwant\n%s", got, want)
	}

	// With all the categories: three rules, then one line per category in order.
	full := strings.Split(BuildSystemPrompt(testCategories), "\n")
	if len(full) != 8+len(testCategories) {
		t.Fatalf("the prompt has %d lines, want 8 plus one per category (%d)", len(full), 8+len(testCategories))
	}
	for i, c := range testCategories {
		if want := "- " + c.Key + " (" + c.Name + "): " + c.Description; full[8+i] != want {
			t.Errorf("category line %d = %q, want %q", i+1, full[8+i], want)
		}
	}
}

func TestBuildUserText(t *testing.T) {
	tests := []struct {
		in   Input
		want string
	}{
		{Input{Title: "Süt al"}, "Başlık: Süt al"},
		{Input{Title: "Süt al", Description: "2 litre"}, "Başlık: Süt al\nAçıklama: 2 litre"},
		{Input{Title: "İş", Description: "satır 1\nsatır 2"}, "Başlık: İş\nAçıklama: satır 1\nsatır 2"},
	}
	for _, tc := range tests {
		if got := BuildUserText(tc.in); got != tc.want {
			t.Errorf("BuildUserText(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
