package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, []byte("hello"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("sha256 = %q", got)
	}
}

func TestCloneMap(t *testing.T) {
	in := map[string]string{"a": "b"}
	out := CloneMap(in)
	out["a"] = "changed"
	if in["a"] != "b" {
		t.Fatal("CloneMap must copy map")
	}
}
