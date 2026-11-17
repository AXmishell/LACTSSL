package api

import (
	"runtime"
	"testing"
	"time"
)

func TestBuildInfoFallsBackWhenLDFlagsMissing(t *testing.T) {
	oldVersion, oldBuildTime, oldBuildArch := Version, BuildTime, BuildArch
	defer func() { Version, BuildTime, BuildArch = oldVersion, oldBuildTime, oldBuildArch }()

	Version, BuildTime, BuildArch = "", "", ""

	version, buildTime, arch := buildInfo()
	if version != "dev" {
		t.Fatalf("Version = %q, want dev", version)
	}
	if arch != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("BuildArch = %q, want %q", arch, runtime.GOOS+"/"+runtime.GOARCH)
	}
	if buildTime == "" {
		t.Fatal("未注入 ldflags 时 BuildTime 应回退到可执行文件修改时间，实际为空")
	}
	if _, err := time.Parse(time.RFC3339, buildTime); err != nil {
		t.Fatalf("BuildTime %q 不是合法的 RFC3339 时间: %v", buildTime, err)
	}
}

func TestBuildInfoPrefersInjectedValues(t *testing.T) {
	oldVersion, oldBuildTime, oldBuildArch := Version, BuildTime, BuildArch
	defer func() { Version, BuildTime, BuildArch = oldVersion, oldBuildTime, oldBuildArch }()

	Version, BuildTime, BuildArch = "1.2.3", "2026-01-02T03:04:05Z", "linux/arm64"

	version, buildTime, arch := buildInfo()
	if version != "1.2.3" || buildTime != "2026-01-02T03:04:05Z" || arch != "linux/arm64" {
		t.Fatalf("buildInfo() = %q, %q, %q", version, buildTime, arch)
	}
}
