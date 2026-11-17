package logging

import (
	"log"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureSinkReceivesComponentAndFields(t *testing.T) {
	restoreLogger(t)
	SetSink(nil)
	t.Cleanup(func() { SetSink(nil) })

	var entries []Entry
	SetSink(func(entry Entry) {
		entries = append(entries, entry)
	})
	closeFn, err := Init(Config{
		Level:      LevelDebug,
		Format:     FormatText,
		File:       filepath.Join(t.TempDir(), "app.log"),
		AlsoStdout: false,
		MaxSizeMB:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()

	Component("service").Info("task failed", "task_id", int64(7), "reason", "timeout")
	log.Print("legacy")

	if len(entries) != 2 {
		t.Fatalf("expected two entries, got %d: %+v", len(entries), entries)
	}
	first := entries[0]
	if first.Component != "service" || first.Message != "task failed" || first.Level != slog.LevelInfo {
		t.Fatalf("unexpected first entry: %+v", first)
	}
	if first.Fields["task_id"] != int64(7) || first.Fields["reason"] != "timeout" {
		t.Fatalf("unexpected fields: %+v", first.Fields)
	}
	if _, exists := first.Fields["component"]; exists {
		t.Fatal("component should not be duplicated in fields")
	}
	if entries[1].Component != "app" || entries[1].Message != "legacy" {
		t.Fatalf("standard log not captured: %+v", entries[1])
	}
}

func TestCaptureBuffersEntriesUntilSinkAttached(t *testing.T) {
	restoreLogger(t)
	SetSink(nil)
	t.Cleanup(func() { SetSink(nil) })

	closeFn, err := Init(Config{
		Level:      LevelInfo,
		Format:     FormatText,
		File:       filepath.Join(t.TempDir(), "app.log"),
		AlsoStdout: false,
		MaxSizeMB:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()

	Component("main").Info("startup")
	var entries []Entry
	SetSink(func(entry Entry) {
		entries = append(entries, entry)
	})
	if len(entries) != 1 || entries[0].Message != "startup" {
		t.Fatalf("pending startup log not flushed: %+v", entries)
	}
}

func TestCaptureIgnoresDebugBelowLevel(t *testing.T) {
	restoreLogger(t)
	SetSink(nil)
	t.Cleanup(func() { SetSink(nil) })

	var entries []Entry
	SetSink(func(entry Entry) {
		entries = append(entries, entry)
	})
	closeFn, err := Init(Config{
		Level:      LevelInfo,
		Format:     FormatText,
		File:       filepath.Join(t.TempDir(), "app.log"),
		AlsoStdout: false,
		MaxSizeMB:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()

	slog.Debug("hidden", "time", time.Now())
	Component("main").Info("visible")
	if len(entries) != 1 || entries[0].Message != "visible" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}
