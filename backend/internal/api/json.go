package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// maxBodyBytes is the largest request body decodeJSON accepts.
const maxBodyBytes = 1 << 20 // 1 MiB

// msgInternal is the only thing a client learns about an unexpected error.
const msgInternal = "internal server error"

// The errors of decodeJSON. Their text is meant for the client.
var (
	errInvalidJSON  = errors.New("invalid JSON body")
	errBodyTooLarge = errors.New("request body too large")
)

// writeJSON sends v as the JSON body of a response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	// Encode before anything is written, so that a failure can still become a 500.
	body, err := json.Marshal(v)
	if err != nil {
		slog.Error("encode response", "error", err)
		status, body = http.StatusInternalServerError, []byte(`{"error":"`+msgInternal+`"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(append(body, '\n'))
}

// writeError sends the error response {"error": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// serverWriter returns the ResponseWriter that the server itself created, found
// below any wrappers that have an Unwrap method. http.MaxBytesReader needs that
// one: when the limit is hit it tells the server to close the connection in an
// orderly way, through a method that a wrapper does not have. Without it an
// upload over the limit that announces itself with "Expect: 100-continue" (curl
// does so above 1 MiB) is answered by a reset connection, and the client never
// sees the 400.
func serverWriter(w http.ResponseWriter) http.ResponseWriter {
	for {
		wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return w
		}
		w = wrapper.Unwrap()
	}
}

// decodeJSON reads the request body, which must be one JSON object of at most
// maxBodyBytes, into dst. Fields dst does not have are an error.
//
// The text of a returned error is meant for the client: "invalid JSON body",
// "request body too large" or `unknown field "<name>"`.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	body, err := io.ReadAll(http.MaxBytesReader(serverWriter(w), r.Body, maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errBodyTooLarge
		}
		return errInvalidJSON
	}
	// Decoding alone would take "null" for an empty object, and would answer
	// "[]", "5" and the like with a message about Go types.
	if !bytes.HasPrefix(bytes.TrimLeft(body, " \t\r\n"), []byte("{")) {
		return errInvalidJSON
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		// encoding/json has no type for this error, only the text "json: unknown field ...".
		if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return fmt.Errorf("unknown field %s", field)
		}
		return errInvalidJSON
	}
	// Nothing but white space may follow the object.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errInvalidJSON
	}
	return nil
}
