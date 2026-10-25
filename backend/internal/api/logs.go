package api

import (
	"net/http"
	"strconv"

	"todoapp/internal/applog"
)

const (
	defaultLogLimit = 200
	maxLogLimit     = 500
)

// logsResponse is the answer of GET /api/logs (step 3 spec 7).
type logsResponse struct {
	Entries   []applog.Entry `json:"entries"`
	LastSeq   int64          `json:"last_seq"` // where the reader continues: after=last_seq
	Truncated bool           `json:"truncated"`
}

// listLogs answers GET /api/logs?after=<n>&limit=<m>: the entries with a seq
// greater than after (default 0), oldest first; if there are more than limit of
// them (1 to 500, default 200), the newest limit, and truncated. Reading the
// log is no event, so it is not logged.
func (s *server) listLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	after := int64(0)
	if query.Has("after") {
		n, err := strconv.ParseInt(query.Get("after"), 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid after")
			return
		}
		after = n
	}
	limit := defaultLogLimit
	if query.Has("limit") {
		n, err := strconv.Atoi(query.Get("limit"))
		if err != nil || n < 1 || n > maxLogLimit {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}

	entries, lastSeq, truncated := s.logs.After(after, limit)
	writeJSON(w, http.StatusOK, logsResponse{Entries: entries, LastSeq: lastSeq, Truncated: truncated})
}
