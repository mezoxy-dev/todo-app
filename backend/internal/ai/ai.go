// Package ai puts a todo into one of the categories with a language model (step
// 3 spec 6). The rest of the app depends on the small Classifier interface
// only; Gemini, which talks to the Gemini API with nothing but net/http, is the
// one implementation so far.
//
// The API key of the service is never part of an error, a result or a log
// entry made here.
package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Category is one of the categories the model chooses from.
type Category struct {
	Key         string // what the model answers with
	Name        string // what the user sees
	Description string // what belongs in the category, with examples
}

// Input is one todo to classify.
type Input struct {
	Title, Description string
	Categories         []Category // the categories to choose from: the inbox is not one of them
}

// Result is the answer of the model.
type Result struct {
	Key      string        // the key of one of Input.Categories
	Reason   string        // one short sentence, in Turkish
	Model    string        // the model that was asked
	Raw      string        // the JSON text the model sent, for the log
	Duration time.Duration // how long the call took
}

// Classifier chooses the category of a todo. An error says what went wrong in a
// way that can be told apart (step 3 spec 6.1): a *QuotaError, an *HTTPError,
// ErrBadResponse, the error of the context (context.DeadlineExceeded when
// Gemini's timeout runs out), or any other error for a connection that failed.
type Classifier interface {
	Classify(ctx context.Context, in Input) (Result, error)
}

// ErrBadResponse is what an error wraps when the service answered but not with
// a usable choice: the text is not the JSON that was asked for, the category is
// not one of the keys that were offered, there is no answer at all, or the
// prompt was blocked.
var ErrBadResponse = errors.New("invalid response from the model")

// QuotaError means that the service refused the call because the quota of the
// key is used up (HTTP 429). The call is not repeated by itself: waiting is up
// to the user (step 3 spec 6.4).
type QuotaError struct {
	RetryAfter string // the Retry-After header of the answer; empty if there was none
	Body       string // the start of the body of the answer, without the API key
}

func (e *QuotaError) Error() string {
	msg := fmt.Sprintf("quota exceeded (HTTP %d)", http.StatusTooManyRequests)
	if e.RetryAfter != "" {
		msg += ", retry after " + e.RetryAfter
	}
	return msg
}

// HTTPError means that the service answered with a status other than 2xx and
// other than 429.
type HTTPError struct {
	Status int
	Body   string // the start of the body of the answer, without the API key
}

func (e *HTTPError) Error() string {
	return strings.TrimSpace(fmt.Sprintf("HTTP %d %s", e.Status, http.StatusText(e.Status)))
}

// promptHeader is the instruction of step 3 spec 6.3, up to the list of
// categories.
const promptHeader = `Sen bir yapılacaklar listesi asistanısın. Kullanıcının yazdığı görevi aşağıdaki kategorilerden
TAM OLARAK BİRİNE yerleştir.
Kurallar:
1. Görev bir şeyi öğrenmeyi, araştırmayı ya da karşılaştırmayı gerektiriyorsa veya ne yapılacağına
   karar vermek için gerekli bilgi verilmemişse "research" seç.
2. Hiçbir kategori uymuyorsa "other" seç.
3. "reason" alanına seçimini açıklayan tek kısa Türkçe cümle yaz.
Kategoriler:`

// BuildSystemPrompt returns the instruction for the model, with one line per
// category in the order given: "- <key> (<name>): <description>". The
// categories come from the database, so editing a description changes what the
// model is told.
func BuildSystemPrompt(cats []Category) string {
	var b strings.Builder
	b.WriteString(promptHeader)
	for _, c := range cats {
		b.WriteString("\n- " + c.Key + " (" + c.Name + "): " + c.Description)
	}
	return b.String()
}

// BuildUserText returns the text that is sent to the model about the todo:
// "Başlık: <title>", and, if there is a description, "Açıklama: <description>"
// on the next line.
func BuildUserText(in Input) string {
	text := "Başlık: " + in.Title
	if in.Description != "" {
		text += "\nAçıklama: " + in.Description
	}
	return text
}
