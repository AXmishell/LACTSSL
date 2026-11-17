package plugininstall

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallReplaceRollbackRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest, backup, err := Install(dir, "lactssl-dns-test", src)
	if err != nil {
		t.Fatal(err)
	}
	if backup != "" {
		t.Fatal("new install should not have backup")
	}
	if data, _ := os.ReadFile(dest); string(data) != "v1" {
		t.Fatalf("content=%q", data)
	}

	src2 := filepath.Join(dir, "src2")
	if err := os.WriteFile(src2, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest2, backup2, err := Install(dir, "lactssl-dns-test", src2)
	if err != nil {
		t.Fatal(err)
	}
	if backup2 == "" {
		t.Fatal("replace should create backup")
	}
	Rollback(dest2, backup2)
	if data, _ := os.ReadFile(dest2); string(data) != "v1" {
		t.Fatalf("rollback content=%q", data)
	}
	Commit(backup2)
	if _, err := os.Stat(backup2); !os.IsNotExist(err) {
		t.Fatal("backup should be removed by Commit")
	}
	if err := Remove(dir, "lactssl-dns-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest2); !os.IsNotExist(err) {
		t.Fatal("plugin file should be removed")
	}
}

func TestSafePathRejectsTraversal(t *testing.T) {
	if _, err := SafePath(t.TempDir(), "../evil"); err == nil {
		t.Fatal("traversal should be rejected")
	}
	if _, err := SafePath(t.TempDir(), "evil/../../x"); err == nil {
		t.Fatal("nested traversal should be rejected")
	}
}
