package applog

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Every Add is also written with slog. Keep that out of the test output; the
	// test that looks at it installs its own handler.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// addN adds n entries with the messages "m1" .. "mn" and returns the last one.
func addN(b *Buffer, n int) Entry {
	var last Entry
	for i := 1; i <= n; i++ {
		last = b.Add(LevelInfo, SourceSystem, "m"+strconv.Itoa(i), "")
	}
	return last
}

// seqs returns the sequence numbers of the entries, in order.
func seqs(entries []Entry) []int64 {
	out := []int64{}
	for _, e := range entries {
		out = append(out, e.Seq)
	}
	return out
}

// seqRange returns from, from+1, ..., to.
func seqRange(from, to int64) []int64 {
	out := []int64{}
	for s := from; s <= to; s++ {
		out = append(out, s)
	}
	return out
}

func TestAddAssignsIncreasingSeq(t *testing.T) {
	b := New(500)
	before := time.Now().UTC().Add(-time.Second)

	var got []Entry
	for _, msg := range []string{"bir", "iki", "üç"} {
		got = append(got, b.Add(LevelInfo, SourceAPI, msg, "ayrıntı "+msg))
	}

	if want := []int64{1, 2, 3}; !slices.Equal(seqs(got), want) {
		t.Errorf("seq = %v, want %v", seqs(got), want)
	}
	for i, e := range got {
		if e.Time.IsZero() {
			t.Errorf("entry %d: Time is zero", e.Seq)
		}
		if e.Time.Location() != time.UTC {
			t.Errorf("entry %d: Time location = %v, want UTC", e.Seq, e.Time.Location())
		}
		if e.Time.Before(before) || e.Time.After(time.Now().UTC().Add(time.Second)) {
			t.Errorf("entry %d: Time = %v, want about now", e.Seq, e.Time)
		}
		if i > 0 && e.Time.Before(got[i-1].Time) {
			t.Errorf("entry %d: Time %v is before the previous entry's %v", e.Seq, e.Time, got[i-1].Time)
		}
	}
	// What Add returns is what After returns.
	stored, lastSeq, truncated := b.After(0, 200)
	if !slices.EqualFunc(stored, got, func(a, b Entry) bool { return a == b }) {
		t.Errorf("After(0, 200) = %+v, want what Add returned: %+v", stored, got)
	}
	if lastSeq != 3 || truncated {
		t.Errorf("lastSeq = %d, truncated = %v; want 3, false", lastSeq, truncated)
	}
	if e := got[1]; e.Level != LevelInfo || e.Source != SourceAPI || e.Message != "iki" || e.Details != "ayrıntı iki" {
		t.Errorf("entry 2 = %+v, want level info, source api, message iki and its details", e)
	}
}

func TestAfterAndLimit(t *testing.T) {
	b := New(500)
	addN(b, 10)

	tests := []struct {
		name          string
		after         int64
		limit         int
		wantSeqs      []int64
		wantLastSeq   int64
		wantTruncated bool
	}{
		{"everything, oldest first", 0, 200, seqRange(1, 10), 10, false},
		{"after 7", 7, 200, seqRange(8, 10), 10, false},
		{"the newest three", 0, 3, seqRange(8, 10), 10, true},
		{"nothing new", 10, 5, []int64{}, 10, false},

		// Beyond the cases of the brief.
		{"exactly the limit is not truncated", 0, 10, seqRange(1, 10), 10, false},
		{"one over the limit", 0, 9, seqRange(2, 10), 10, true},
		{"the limit applies after the cursor", 4, 3, seqRange(8, 10), 10, true},
		{"after the last entry", 11, 5, []int64{}, 10, false},
		{"after is huge", math.MaxInt64, 5, []int64{}, 10, false},
		{"a negative cursor means from the start", -5, 200, seqRange(1, 10), 10, false},
		{"limit one", 0, 1, []int64{10}, 10, true},
		{"limit zero", 0, 0, []int64{}, 10, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entries, lastSeq, truncated := b.After(tc.after, tc.limit)

			if entries == nil {
				t.Error("entries is nil, want a slice that encodes as []")
			}
			if got := seqs(entries); !slices.Equal(got, tc.wantSeqs) {
				t.Errorf("seqs = %v, want %v", got, tc.wantSeqs)
			}
			if lastSeq != tc.wantLastSeq || truncated != tc.wantTruncated {
				t.Errorf("lastSeq = %d, truncated = %v; want %d, %v", lastSeq, truncated, tc.wantLastSeq, tc.wantTruncated)
			}
			for _, e := range entries {
				if want := "m" + strconv.FormatInt(e.Seq, 10); e.Message != want {
					t.Errorf("entry %d has message %q, want %q", e.Seq, e.Message, want)
				}
			}
		})
	}
}

func TestAfterOnEmptyBuffer(t *testing.T) {
	b := New(500)

	entries, lastSeq, truncated := b.After(0, 200)

	if entries == nil || len(entries) != 0 {
		t.Errorf("entries = %#v, want an empty, non-nil slice", entries)
	}
	if lastSeq != 0 || truncated {
		t.Errorf("lastSeq = %d, truncated = %v; want 0, false", lastSeq, truncated)
	}
}

// A capacity below one would make the ring unusable; the buffer holds one entry.
func TestNewWithoutCapacity(t *testing.T) {
	for _, capacity := range []int{0, -3} {
		b := New(capacity)

		b.Add(LevelInfo, SourceSystem, "ilk", "")
		last := b.Add(LevelInfo, SourceSystem, "son", "")

		entries, lastSeq, _ := b.After(0, 10)
		if last.Seq != 2 || lastSeq != 2 || len(entries) != 1 || entries[0].Message != "son" {
			t.Errorf("New(%d): entries %+v, lastSeq %d; want only the newest entry", capacity, entries, lastSeq)
		}
	}
}

func TestCapacityDropsOldest(t *testing.T) {
	b := New(500)

	last := addN(b, 501)

	entries, lastSeq, truncated := b.After(0, 500)
	if got, want := seqs(entries), seqRange(2, 501); !slices.Equal(got, want) {
		t.Errorf("seqs = %v..%v (%d entries), want 2..501", got[0], got[len(got)-1], len(got))
	}
	if lastSeq != 501 || lastSeq != last.Seq {
		t.Errorf("lastSeq = %d, want 501", lastSeq)
	}
	// The entry that was dropped is not "more to come": there is nothing to fetch.
	if truncated {
		t.Error("truncated = true, want false: the dropped entry cannot be fetched with a bigger limit")
	}
	if entries[0].Message != "m2" || entries[len(entries)-1].Message != "m501" {
		t.Errorf("first message %q, last message %q; want m2 and m501", entries[0].Message, entries[len(entries)-1].Message)
	}

	// After many laps the buffer holds the newest entries, in order.
	addN(b, 1200)
	entries, lastSeq, _ = b.After(0, 500)
	if got, want := seqs(entries), seqRange(1701-499, 1701); !slices.Equal(got, want) {
		t.Errorf("after 1701 entries: seqs = %v..%v (%d entries), want 1202..1701", got[0], got[len(got)-1], len(got))
	}
	if lastSeq != 1701 {
		t.Errorf("lastSeq = %d, want 1701", lastSeq)
	}
	// A reader that is slower than the buffer gets what is left of what it asked for.
	entries, _, _ = b.After(1000, 500)
	if got, want := seqs(entries), seqRange(1202, 1701); !slices.Equal(got, want) {
		t.Errorf("After(1000): seqs = %v..%v, want 1202..1701", got[0], got[len(got)-1])
	}
}

func TestConcurrentAdd(t *testing.T) {
	const goroutines, perGoroutine = 20, 25
	b := New(500)

	results := make([][]int64, goroutines)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := range goroutines {
		wg.Go(func() {
			<-start
			for i := range perGoroutine {
				e := b.Add(LevelInfo, SourceAPI, "g"+strconv.Itoa(g)+"-"+strconv.Itoa(i), "")
				results[g] = append(results[g], e.Seq)
			}
		})
	}
	// Readers run next to the writers.
	for range 4 {
		wg.Go(func() {
			<-start
			for range 50 {
				b.After(0, 200)
			}
		})
	}
	close(start)
	wg.Wait()

	var all []int64
	for g, got := range results {
		if !slices.IsSorted(got) {
			t.Errorf("goroutine %d got seqs %v, want them increasing", g, got)
		}
		all = append(all, got...)
	}
	slices.Sort(all)
	if want := seqRange(1, goroutines*perGoroutine); !slices.Equal(all, want) {
		t.Errorf("the seqs handed out are not 1..%d without a gap or a duplicate", goroutines*perGoroutine)
	}
	entries, lastSeq, truncated := b.After(0, 500)
	if got, want := seqs(entries), seqRange(1, goroutines*perGoroutine); !slices.Equal(got, want) {
		t.Errorf("After(0, 500) holds %d entries, want 1..%d in order", len(got), goroutines*perGoroutine)
	}
	if lastSeq != goroutines*perGoroutine || truncated {
		t.Errorf("lastSeq = %d, truncated = %v; want %d, false", lastSeq, truncated, goroutines*perGoroutine)
	}
}

// Spec 8: every entry is also written to the console with slog, at the level of
// the entry.
func TestAddAlsoLogsWithSlog(t *testing.T) {
	var out bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	b := New(10)

	b.Add(LevelInfo, SourceAPI, "Todo eklendi: “Süt al”", "")
	b.Add(LevelWarn, SourceAI, "Öneri uygulanmadı", "Önerilen kategori: work")
	b.Add(LevelError, SourceSystem, "Sınıflandırılamadı", "ayrıntı")

	type record struct {
		Level   string `json:"level"`
		Msg     string `json:"msg"`
		Source  string `json:"source"`
		Seq     int64  `json:"seq"`
		Details string `json:"details"`
	}
	var got []record
	dec := json.NewDecoder(&out)
	for dec.More() {
		var r record
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("slog output is not JSON: %v", err)
		}
		got = append(got, r)
	}
	want := []record{
		{"INFO", "Todo eklendi: “Süt al”", "api", 1, ""},
		{"WARN", "Öneri uygulanmadı", "ai", 2, "Önerilen kategori: work"},
		{"ERROR", "Sınıflandırılamadı", "system", 3, "ayrıntı"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("slog records = %+v, want %+v", got, want)
	}
}

// Spec 8: the JSON shape of an entry; details are left out when there are none.
func TestEntryJSON(t *testing.T) {
	b := New(10)
	plain := b.Add(LevelInfo, SourceSystem, "Sunucu başladı (model: m)", "")
	detailed := b.Add(LevelError, SourceAI, "Sınıflandırılamadı: “x”", "satır 1\nsatır 2")

	asMap := func(e Entry) map[string]any {
		t.Helper()
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		return m
	}
	gotPlain, gotDetailed := asMap(plain), asMap(detailed)

	if _, ok := gotPlain["details"]; ok {
		t.Errorf("entry without details encodes as %v, want no details key", gotPlain)
	}
	for key, want := range map[string]any{"seq": float64(1), "level": "info", "source": "system", "message": "Sunucu başladı (model: m)"} {
		if gotPlain[key] != want {
			t.Errorf("%s = %v, want %v", key, gotPlain[key], want)
		}
	}
	if s, _ := gotPlain["time"].(string); s == "" {
		t.Errorf("time = %v, want an RFC 3339 string", gotPlain["time"])
	} else if ts, err := time.Parse(time.RFC3339Nano, s); err != nil || ts.Location() != time.UTC {
		t.Errorf("time = %q, want an RFC 3339 timestamp in UTC (%v)", s, err)
	}
	if gotDetailed["details"] != "satır 1\nsatır 2" {
		t.Errorf("details = %v, want the two lines", gotDetailed["details"])
	}
}

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Süt al", "“Süt al”"},
		{"", "“”"},
		// A message is one line, whatever the title holds.
		{"iki\nsatır", "“iki satır”"},
		{"  boşluk\t\tve\r\nsekme  ", "“boşluk ve sekme”"},
	}
	for _, tc := range tests {
		if got := Quote(tc.in); got != tc.want {
			t.Errorf("Quote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
