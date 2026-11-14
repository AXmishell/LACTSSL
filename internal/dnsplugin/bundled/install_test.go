package bundled

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallWritesEmbeddedHelper(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dns")
	data := []byte("#!/bin/sh\necho helper\n")
	path, installed, err := install(data, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("first install should write the helper")
	}
	if path != filepath.Join(dir, PathName()) {
		t.Fatalf("unexpected path: %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("helper content = %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("helper should be executable: %v", info.Mode())
		}
	}

	// 内容一致时不重复写入。
	if _, installed, err := install(data, dir); err != nil || installed {
		t.Fatalf("second install = %v, %v", installed, err)
	}

	// 内容变化时更新为内置版本。
	updated := []byte("#!/bin/sh\necho updated\n")
	if _, installed, err := install(updated, dir); err != nil || !installed {
		t.Fatalf("update install = %v, %v", installed, err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(updated) {
		t.Fatalf("updated content = %q", got)
	}
}

func TestInstallSkipsWhenNotEmbedded(t *testing.T) {
	dir := t.TempDir()
	if path, installed, err := install(nil, dir); err != nil || installed || path != "" {
		t.Fatalf("empty helper = %q, %v, %v", path, installed, err)
	}
	if _, installed, err := install([]byte("x"), "  "); err != nil || installed {
		t.Fatalf("empty dir = %v, %v", installed, err)
	}
}

func TestInstallRejectsNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, PathName())); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, _, err := install([]byte("data"), dir); err == nil {
		t.Fatal("symlink at destination should be rejected")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "secret" {
		t.Fatalf("symlink target should not be modified: %q, %v", got, err)
	}
}
