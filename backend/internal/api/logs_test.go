package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"todoapp/internal/applog"
)

// entryJSON mirrors the Entry shape of step 3 spec 8, and logsJSON the answer of
// GET /api/logs of spec 7. The tests keep their own copy of the wire format.
type entryJSON struct {
	Seq     int64  `json:"seq"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Source  string `json:"source"`
	Message string `json:"message"`
	Details string `json:"details"`
}

type logsJSON struct {
	Entries   []entryJSON `json:"entries"`
	LastSeq   int64       `json:"last_seq"`
	Truncated bool        `json:"truncated"`
}

func (l logsJSON) seqs() []int64 {
	seqs := []int64{}
	for _, e := range l.Entries {
		seqs = append(seqs, e.Seq)
	}
	return seqs
}

// seqRange returns from, from+1, ..., to.
func seqRange(from, to int64) []int64 {
	seqs := []int64{}
	for s := from; s <= to; s++ {
		seqs = append(seqs, s)
	}
	return seqs
}

// getLogs sends GET /api/logs with the query and decodes the answer.
func getLogs(t *testing.T, env *testEnv, query string) logsJSON {
	t.Helper()
	rec := do(t, env.h, http.MethodGet, "/api/logs"+query, "")
	wantJSON(t, rec, http.StatusOK)
	return decodeBody[logsJSON](t, rec)
}

// addEntries adds n entries with the messages "m1" .. "mn" to the log.
func addEntries(env *testEnv, n int) {
	for i := 1; i <= n; i++ {
		env.logs.Add(applog.LevelInfo, applog.SourceSystem, fmt.Sprintf("m%d", i), "")
	}
}

// Step 3 spec 7: entries from `after` on, oldest first; more than `limit` of
// them means the newest `limit` and truncated.
func TestLogsEndpoint(t *testing.T) {
	env := newTestEnv(t)
	env.logs.Add(applog.LevelInfo, applog.SourceSystem, "Sunucu başladı (model: m)", "")
	env.logs.Add(applog.LevelWarn, applog.SourceAI, "Öneri uygulanmadı: “x” (kart elle taşınmıştı)", "Önerilen kategori: work")
	env.logs.Add(applog.LevelError, applog.SourceAI, "Sınıflandırılamadı: “y”", "Hata türü: kota aşıldı\nHTTP durumu: 429")

	all := getLogs(t, env, "")

	if got, want := all.seqs(), []int64{1, 2, 3}; !slices.Equal(got, want) {
		t.Errorf("seqs = %v, want %v (oldest first)", got, want)
	}
	if all.LastSeq != 3 || all.Truncated {
		t.Errorf("last_seq = %d, truncated = %v; want 3, false", all.LastSeq, all.Truncated)
	}
	want := []entryJSON{
		{Seq: 1, Level: "info", Source: "system", Message: "Sunucu başladı (model: m)"},
		{Seq: 2, Level: "warn", Source: "ai", Message: "Öneri uygulanmadı: “x” (kart elle taşınmıştı)", Details: "Önerilen kategori: work"},
		{Seq: 3, Level: "error", Source: "ai", Message: "Sınıflandırılamadı: “y”", Details: "Hata türü: kota aşıldı\nHTTP durumu: 429"},
	}
	for i, got := range all.Entries {
		ts, err := time.Parse(time.RFC3339Nano, got.Time)
		if err != nil || !strings.HasSuffix(got.Time, "Z") || time.Since(ts) > time.Minute || time.Since(ts) < -time.Minute {
			t.Errorf("entry %d: time = %q, want a timestamp of about now in UTC (%v)", got.Seq, got.Time, err)
		}
		got.Time = ""
		if got != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i+1, got, want[i])
		}
	}

	tests := []struct {
		query         string
		wantSeqs      []int64
		wantTruncated bool
	}{
		{"?after=2", []int64{3}, false},
		{"?limit=2", []int64{2, 3}, true}, // the newest two
		{"?after=1&limit=1", []int64{3}, true},
		{"?after=0&limit=3", []int64{1, 2, 3}, false},
		{"?after=3", []int64{}, false},
		{"?after=100", []int64{}, false},
		{"?after=0&limit=500", []int64{1, 2, 3}, false},
		{"?limit=1", []int64{3}, true},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			got := getLogs(t, env, tc.query)

			if !slices.Equal(got.seqs(), tc.wantSeqs) {
				t.Errorf("seqs = %v, want %v", got.seqs(), tc.wantSeqs)
			}
			if got.LastSeq != 3 || got.Truncated != tc.wantTruncated {
				t.Errorf("last_seq = %d, truncated = %v; want 3, %v", got.LastSeq, got.Truncated, tc.wantTruncated)
			}
		})
	}
}

// The wire format: the three keys of the answer, entries that are [] and not
// null when there are none, and details left out when an entry has none.
func TestLogsEndpointShape(t *testing.T) {
	env := newTestEnv(t)

	rec := do(t, env.h, http.MethodGet, "/api/logs", "")
	wantJSON(t, rec, http.StatusOK)
	if got := strings.TrimSpace(rec.Body.String()); got != `{"entries":[],"last_seq":0,"truncated":false}` {
		t.Errorf("empty log: body = %s, want %s", got, `{"entries":[],"last_seq":0,"truncated":false}`)
	}

	env.logs.Add(applog.LevelInfo, applog.SourceAPI, "Todo eklendi: “A”", "")
	env.logs.Add(applog.LevelInfo, applog.SourceAI, "Sınıflandırıldı", "ayrıntı")
	rec = do(t, env.h, http.MethodGet, "/api/logs?after=3", "")
	if got := strings.TrimSpace(rec.Body.String()); got != `{"entries":[],"last_seq":2,"truncated":false}` {
		t.Errorf("nothing new: body = %s, want %s", got, `{"entries":[],"last_seq":2,"truncated":false}`)
	}

	rec = do(t, env.h, http.MethodGet, "/api/logs", "")
	var body struct {
		Entries []map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Entries) != 2 {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	fields := func(entry map[string]json.RawMessage) []string {
		out := []string{}
		for k := range entry {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	if got, want := fields(body.Entries[0]), []string{"level", "message", "seq", "source", "time"}; !slices.Equal(got, want) {
		t.Errorf("entry without details has the fields %v, want %v", got, want)
	}
	if got, want := fields(body.Entries[1]), []string{"details", "level", "message", "seq", "source", "time"}; !slices.Equal(got, want) {
		t.Errorf("entry with details has the fields %v, want %v", got, want)
	}
}

// Step 3 spec 7: limit is 200 unless it says otherwise, and at most 500.
func TestLogsEndpointLimits(t *testing.T) {
	env := newTestEnv(t)
	addEntries(env, 250)

	byDefault := getLogs(t, env, "")
	if !slices.Equal(byDefault.seqs(), seqRange(51, 250)) || !byDefault.Truncated || byDefault.LastSeq != 250 {
		t.Errorf("default: seqs %d..%d (%d entries), truncated %v, last_seq %d; want the newest 200 (51..250), truncated, 250",
			byDefault.Entries[0].Seq, byDefault.Entries[len(byDefault.Entries)-1].Seq, len(byDefault.Entries), byDefault.Truncated, byDefault.LastSeq)
	}
	most := getLogs(t, env, "?limit=500")
	if !slices.Equal(most.seqs(), seqRange(1, 250)) || most.Truncated {
		t.Errorf("limit=500: %d entries, truncated %v; want all 250, not truncated", len(most.Entries), most.Truncated)
	}
	// Reading on from where the last answer ended gives what came since.
	addEntries(env, 3)
	more := getLogs(t, env, "?after=250")
	if !slices.Equal(more.seqs(), []int64{251, 252, 253}) || more.LastSeq != 253 || more.Truncated {
		t.Errorf("after=250: seqs %v, last_seq %d, truncated %v; want 251..253, 253, false", more.seqs(), more.LastSeq, more.Truncated)
	}
}

func TestLogsEndpointValidation(t *testing.T) {
	env := newTestEnv(t)
	addEntries(env, 3)

	tests := []struct {
		name, query, want string
	}{
		{"negative after", "?after=-1", "invalid after"},
		{"after is not a number", "?after=x", "invalid after"},
		{"empty after", "?after=", "invalid after"},
		{"fractional after", "?after=1.5", "invalid after"},
		{"after too large", "?after=99999999999999999999", "invalid after"},
		{"limit zero", "?limit=0", "invalid limit"},
		{"limit over 500", "?limit=501", "invalid limit"},
		{"limit is not a number", "?limit=x", "invalid limit"},
		{"empty limit", "?limit=", "invalid limit"},
		{"negative limit", "?limit=-5", "invalid limit"},
		{"fractional limit", "?limit=2.5", "invalid limit"},
		{"limit too large for an integer", "?limit=99999999999999999999", "invalid limit"},
		{"both wrong: after is reported", "?after=x&limit=0", "invalid after"},
		{"a good after next to a bad limit", "?after=1&limit=0", "invalid limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, env.h, http.MethodGet, "/api/logs"+tt.query, "")

			wantError(t, rec, http.StatusBadRequest, tt.want)
		})
	}
}

// Step 3 spec 7 and 8: reading the log or the todos is not an event. The panel
// polls every second; if its own requests were logged it would never be quiet.
func TestGetRequestsAddNoLogEntries(t *testing.T) {
	env := newTestEnv(t)
	todo := mustCreate(t, env.h, `{"title":"Süt al"}`)
	mark := env.mark()

	for _, path := range []string{
		"/api/health",
		"/api/categories",
		"/api/todos",
		todoPath(todo.ID),
		"/api/logs",
		"/api/logs?after=1&limit=1",
		"/api/todos/9999",    // an error is no event either
		"/api/logs?after=-1", // nor is a bad request
		"/api/nope",
	} {
		do(t, env.h, http.MethodGet, path, "")
	}

	if got := env.since(mark); len(got) != 0 {
		t.Errorf("GET requests added the log entries %+v, want none", got)
	}
	if got := env.mark(); got != mark {
		t.Errorf("last_seq moved from %d to %d", mark, got)
	}
}
