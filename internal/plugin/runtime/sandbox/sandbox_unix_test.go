//go:build linux

package sandbox

import (
	"errors"
	"os"
	"testing"
)

func TestRunRefusesRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("仅 root 环境验证兜底拒绝")
	}
	err := Run(Spec{WritableDir: t.TempDir()}, []string{"/bin/true"})
	if !errors.Is(err, ErrRoot) {
		t.Fatalf("Run 应以 root 拒绝执行，实际: %v", err)
	}
}
