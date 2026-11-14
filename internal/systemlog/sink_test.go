package systemlog

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/logging"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "systemlog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("systemlog-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return store.New(conn, cipher)
}

func TestSinkFiltersAndPersists(t *testing.T) {
	st := newTestStore(t)
	sink := New(st, Options{MinLevel: slog.LevelInfo, IncludeAccess: false})
	now := time.Now().UTC()
	sink.Write(logging.Entry{Time: now, Level: slog.LevelDebug, Component: "service", Message: "debug"})
	sink.Write(logging.Entry{Time: now, Level: slog.LevelInfo, Component: "service", Message: "task failed", Fields: map[string]any{"task_id": int64(7)}})
	sink.Write(logging.Entry{Time: now, Level: slog.LevelInfo, Component: "http", Message: "http request"})
	sink.Write(logging.Entry{Time: now, Level: slog.LevelError, Component: "http", Message: "panic recovered", Fields: map[string]any{"request_id": "abc"}})
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	list, total, err := st.ListSystemLogs(store.SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("unexpected system logs: total=%d list=%+v", total, list)
	}
	if list[0].Level != "error" || list[0].Component != "http" || list[0].Message != "panic recovered" {
		t.Fatalf("unexpected first entry: %+v", list[0])
	}
	if list[1].Level != "info" || list[1].Component != "service" || list[1].Message != "task failed" {
		t.Fatalf("unexpected second entry: %+v", list[1])
	}
	if list[1].Fields != `{"task_id":7}` {
		t.Fatalf("unexpected fields: %s", list[1].Fields)
	}
}

func TestSinkCanIncludeAccessLogs(t *testing.T) {
	st := newTestStore(t)
	sink := New(st, Options{MinLevel: slog.LevelDebug, IncludeAccess: true})
	sink.Write(logging.Entry{Time: time.Now().UTC(), Level: slog.LevelInfo, Component: "http", Message: "http request"})
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	_, total, err := st.ListSystemLogs(store.SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("access log should be persisted: total=%d", total)
	}
}

func TestBuildEntrySanitizesFields(t *testing.T) {
	long := make([]byte, 0, 9000)
	for len(long) < 9000 {
		long = append(long, 'x')
	}
	entry := buildEntry(logging.Entry{
		Time:      time.Now().UTC(),
		Level:     slog.LevelError,
		Component: " ",
		Message:   "boom",
		Fields: map[string]any{
			"stack": string(long),
			"nil":   nil,
			"bool":  true,
			"error": &testError{},
		},
	})
	if entry.Component != "app" {
		t.Fatalf("component not normalized: %q", entry.Component)
	}
	if len(entry.Fields) > 9000 {
		t.Fatalf("fields not truncated: %d", len(entry.Fields))
	}
	if entry.Level != "error" {
		t.Fatalf("level not normalized: %q", entry.Level)
	}
}

type testError struct{}

func (e *testError) Error() string { return "test error" }
