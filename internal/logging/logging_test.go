package logging

import (
	"compress/gzip"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigNormalizeAndValidate(t *testing.T) {
	cfg := Config{Level: "WARNING", Format: "JSON"}.Normalize()
	if cfg.Level != LevelWarn || cfg.Format != FormatJSON {
		t.Fatalf("unexpected normalized config: %+v", cfg)
	}
	if cfg.MaxSizeMB != DefaultConfig().MaxSizeMB {
		t.Fatalf("empty max size should use default: %+v", cfg)
	}
	if cfg.MaxBackups != 0 || cfg.MaxAgeDays != 0 {
		t.Fatalf("zero limits should mean unlimited: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	bad := Config{Level: "verbose", Format: FormatText, MaxSizeMB: 1}
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid level should be rejected")
	}
	if Level("warning") != slog.LevelWarn {
		t.Fatal("warning should map to warn")
	}
}

func TestInitWritesConfiguredFileAndBridgesStdLog(t *testing.T) {
	restoreLogger(t)
	path := filepath.Join(t.TempDir(), "logs", "app.log")
	closeFn, err := Init(Config{
		Level:      LevelDebug,
		Format:     FormatJSON,
		File:       path,
		AlsoStdout: false,
		MaxSizeMB:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	slog.Debug("debug message", "answer", 42)
	log.Print("legacy message")
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"msg":"debug message"`) || !strings.Contains(content, `"level":"DEBUG"`) {
		t.Fatalf("debug log not written: %s", content)
	}
	if !strings.Contains(content, `"msg":"legacy message"`) {
		t.Fatalf("standard log not bridged: %s", content)
	}
	if strings.Contains(content, "secret") {
		t.Fatal("unexpected content")
	}
}

func TestRotatingWriterRotatesAndCompresses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writer, err := newRotatingWriter(path, rotateOptions{
		MaxSize:    10,
		MaxBackups: 2,
		Compress:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	if _, err := writer.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("world!")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	matches, err := filepath.Glob(path + ".*.gz")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one compressed backup, got %v", matches)
	}
	file, err := os.Open(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected backup content: %q", data)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "world!" {
		t.Fatalf("unexpected current log content: %q", current)
	}
}

func TestRotatingWriterCleanupByCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	writer, err := newRotatingWriter(path, rotateOptions{MaxSize: 10, MaxBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	tick := 0
	writer.now = func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Second)
	}
	for i := 0; i < 4; i++ {
		if _, err := writer.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	matches, err := filepath.Glob(path + ".*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected two backups, got %v", matches)
	}
}

func restoreLogger(t *testing.T) {
	t.Helper()
	previous := slog.Default()
	writer := log.Writer()
	flags := log.Flags()
	prefix := log.Prefix()
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
	})
}
