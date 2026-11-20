package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func TestCompareProgramVersion(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.2.0", "1.1.9", 1},
		{"1.2.0", "v1.2.0", 0},
		{"1.2.0", "1.2.0-rc.1", 1},
		{"1.2.0-rc.2", "1.2.0-rc.1", 1},
		{"1.2.0-rc.10", "1.2.0-rc.9", 1},
		{"1.2.0-1-gabcd", "1.2.0", -1},
		{"1.2", "1.2.0", 0},
		{"dev", "1.0.0", 0},
	}
	for _, item := range cases {
		if got := compareProgramVersion(item.left, item.right); got != item.want {
			t.Fatalf("compareProgramVersion(%q, %q) = %d, want %d", item.left, item.right, got, item.want)
		}
	}
}

func TestSourceBuildVersionsAreNotUpdatable(t *testing.T) {
	for _, value := range []string{"dev", "1.2.0-3-gabc1234", "1.2.0-3-gabc1234-dirty", "1.2.0-dirty"} {
		if !isSourceBuildVersion(value) {
			t.Fatalf("%q 应识别为源码构建", value)
		}
	}
	for _, value := range []string{"1.2.0", "v1.2.0", "1.2.0-rc.1", "1.2.0-beta.2"} {
		if isSourceBuildVersion(value) {
			t.Fatalf("%q 不应识别为源码构建", value)
		}
	}
}

func TestSelectProgramAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "lactssl_1.2.0_linux_arm64.tar.gz"},
		{Name: "lactssl_1.2.0_linux_amd64.tar.gz"},
		{Name: "lactssl_1.2.0_windows_amd64.zip"},
	}
	asset, ok := selectProgramAsset(assets, "1.2.0", "linux", "amd64")
	if !ok || asset.Name != "lactssl_1.2.0_linux_amd64.tar.gz" {
		t.Fatalf("未选中正确资产: %+v", asset)
	}
	asset, ok = selectProgramAsset(assets, "1.2.0", "windows", "amd64")
	if !ok || asset.Name != "lactssl_1.2.0_windows_amd64.zip" {
		t.Fatalf("未选中 Windows 资产: %+v", asset)
	}
	if _, ok := selectProgramAsset(assets, "1.2.0", "darwin", "arm64"); ok {
		t.Fatal("不存在的平台不应匹配资产")
	}
}

func TestApplyReplacesBinaryAndKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "lactssl")
	oldBinary := append([]byte{0x7f, 'E', 'L', 'F', 1, 2, 3}, []byte("old")...)
	if err := os.WriteFile(exePath, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	newBinary := append([]byte{0x7f, 'E', 'L', 'F', 4, 5, 6}, []byte("new-version")...)
	archive := buildTarGz(t, "lactssl", newBinary)
	digest := sha256.Sum256(archive)
	checksums := []byte(fmt.Sprintf("%s  lactssl_1.1.0_linux_amd64.tar.gz\n", hex.EncodeToString(digest[:])))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/lactssl/releases/latest":
			payload := ghRelease{
				TagName:     "v1.1.0",
				Name:        "v1.1.0",
				Body:        "- 新增程序升级",
				HTMLURL:     "https://example.com/release",
				PublishedAt: mustParseTime(t, "2026-01-02T03:04:05Z"),
				Assets: []ghAsset{
					{Name: "lactssl_1.1.0_linux_amd64.tar.gz", BrowserDownloadURL: serverURL(r) + "/dl/archive", Size: int64(len(archive))},
					{Name: "checksums.txt", BrowserDownloadURL: serverURL(r) + "/dl/checksums", Size: int64(len(checksums))},
				},
			}
			_ = json.NewEncoder(w).Encode(payload)
		case "/dl/archive":
			_, _ = w.Write(archive)
		case "/dl/checksums":
			_, _ = w.Write(checksums)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	st := newProgramTestStore(t)
	updater := newProgramTestUpdater(t, exePath, dir, server.URL, st)
	status := updater.Status(context.Background(), true)
	if status.CheckError != "" {
		t.Fatalf("检查版本失败: %s", status.CheckError)
	}
	if !status.UpdateAvailable || !status.CanUpdate {
		t.Fatalf("应检测到可升级: %+v", status)
	}
	if status.Latest == nil || status.Latest.Version != "1.1.0" {
		t.Fatalf("最新版本不正确: %+v", status.Latest)
	}
	if status.Latest.Asset.URL == "" || status.Latest.Checksum.URL == "" {
		t.Fatalf("发布资产解析不完整: %+v", status.Latest)
	}

	report, err := updater.Apply(context.Background(), "1.1.0")
	if err != nil {
		t.Fatalf("执行升级失败: %v", err)
	}
	if !report.RestartRequired || report.ToVersion != "1.1.0" {
		t.Fatalf("升级结果不正确: %+v", report)
	}
	installed, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, newBinary) {
		t.Fatalf("二进制未被替换: %q", installed)
	}
	if info, err := os.Stat(exePath); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("新二进制缺少执行权限: %v %v", info, err)
	}
	if _, err := os.Stat(report.BackupPath); err != nil {
		t.Fatalf("升级备份不存在: %v", err)
	}
	backup, err := os.ReadFile(report.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, oldBinary) {
		t.Fatal("备份内容不是升级前的二进制")
	}
	last := updater.lastRun()
	if last == nil || last.ToVersion != "1.1.0" || last.Error != "" {
		t.Fatalf("最近一次升级记录不正确: %+v", last)
	}
	if !updater.RestartPending() {
		t.Fatal("替换二进制后应等待重启")
	}
}

func TestApplyRejectsMissingChecksum(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "lactssl")
	oldBinary := []byte{0x7f, 'E', 'L', 'F', 1, 2, 3}
	if err := os.WriteFile(exePath, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	newBinary := []byte{0x7f, 'E', 'L', 'F', 4, 5, 6}
	archive := buildTarGz(t, "lactssl", newBinary)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/lactssl/releases/latest":
			payload := ghRelease{
				TagName: "v1.1.0",
				Assets: []ghAsset{
					{Name: "lactssl_1.1.0_linux_amd64.tar.gz", BrowserDownloadURL: serverURL(r) + "/dl/archive", Size: int64(len(archive))},
				},
			}
			_ = json.NewEncoder(w).Encode(payload)
		case "/dl/archive":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	updater := newProgramTestUpdater(t, exePath, dir, server.URL, nil)
	_, err := updater.Apply(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("缺少校验和时应拒绝升级，实际错误: %v", err)
	}
	installed, readErr := os.ReadFile(exePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(installed, oldBinary) {
		t.Fatal("校验失败后不应替换二进制")
	}
}

func TestApplyVerifiesChecksumSignature(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "lactssl")
	if err := os.WriteFile(exePath, []byte{0x7f, 'E', 'L', 'F', 1}, 0o755); err != nil {
		t.Fatal(err)
	}
	newBinary := append([]byte{0x7f, 'E', 'L', 'F', 4}, []byte("signed")...)
	archive := buildTarGz(t, "lactssl", newBinary)
	digest := sha256.Sum256(archive)
	checksums := []byte(fmt.Sprintf("%s  lactssl_1.1.0_linux_amd64.tar.gz\n", hex.EncodeToString(digest[:])))

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(priv, checksums)
	sigB64 := base64.StdEncoding.EncodeToString(signature)
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/lactssl/releases/latest":
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v1.1.0",
				Assets: []ghAsset{
					{Name: "lactssl_1.1.0_linux_amd64.tar.gz", BrowserDownloadURL: serverURL(r) + "/dl/archive", Size: int64(len(archive))},
					{Name: "checksums.txt", BrowserDownloadURL: serverURL(r) + "/dl/checksums", Size: int64(len(checksums))},
					{Name: "checksums.txt.sig", BrowserDownloadURL: serverURL(r) + "/dl/checksums.sig", Size: int64(len(sigB64))},
				},
			})
		case "/dl/archive":
			_, _ = w.Write(archive)
		case "/dl/checksums":
			_, _ = w.Write(checksums)
		case "/dl/checksums.sig":
			_, _ = w.Write([]byte(sigB64))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	updater, err := NewProgramUpdater(ProgramOptions{
		CurrentVersion: "1.0.0",
		Enabled:        true,
		Repo:           "acme/lactssl",
		APIBase:        server.URL,
		DataDir:        dir,
		ExePath:        exePath,
		GOOS:           "linux",
		GOARCH:         "amd64",
		MaxDownloadMB:  16,
		PublicKey:      pubB64,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := updater.Apply(context.Background(), "1.1.0")
	if err != nil {
		t.Fatalf("签名校验通过时升级应成功: %v", err)
	}
	if !report.RestartRequired {
		t.Fatal("升级后应等待重启")
	}
	installed, err := os.ReadFile(exePath)
	if err != nil || !bytes.Equal(installed, newBinary) {
		t.Fatalf("二进制未被替换: %v", err)
	}
}

func TestApplyRejectsInvalidChecksumSignature(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "lactssl")
	oldBinary := []byte{0x7f, 'E', 'L', 'F', 1}
	if err := os.WriteFile(exePath, oldBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	newBinary := []byte{0x7f, 'E', 'L', 'F', 9}
	archive := buildTarGz(t, "lactssl", newBinary)
	digest := sha256.Sum256(archive)
	checksums := []byte(fmt.Sprintf("%s  lactssl_1.1.0_linux_amd64.tar.gz\n", hex.EncodeToString(digest[:])))

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// 用另一把私钥签名，模拟 checksums.txt 与签名被替换。
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sigB64 := base64.StdEncoding.EncodeToString(ed25519.Sign(otherPriv, checksums))
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/lactssl/releases/latest":
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v1.1.0",
				Assets: []ghAsset{
					{Name: "lactssl_1.1.0_linux_amd64.tar.gz", BrowserDownloadURL: serverURL(r) + "/dl/archive", Size: int64(len(archive))},
					{Name: "checksums.txt", BrowserDownloadURL: serverURL(r) + "/dl/checksums", Size: int64(len(checksums))},
					{Name: "checksums.txt.sig", BrowserDownloadURL: serverURL(r) + "/dl/checksums.sig", Size: int64(len(sigB64))},
				},
			})
		case "/dl/archive":
			_, _ = w.Write(archive)
		case "/dl/checksums":
			_, _ = w.Write(checksums)
		case "/dl/checksums.sig":
			_, _ = w.Write([]byte(sigB64))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	updater, err := NewProgramUpdater(ProgramOptions{
		CurrentVersion: "1.0.0",
		Enabled:        true,
		Repo:           "acme/lactssl",
		APIBase:        server.URL,
		DataDir:        dir,
		ExePath:        exePath,
		GOOS:           "linux",
		GOARCH:         "amd64",
		MaxDownloadMB:  16,
		PublicKey:      pubB64,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = updater.Apply(context.Background(), "1.1.0")
	if err == nil || !strings.Contains(err.Error(), "签名") {
		t.Fatalf("签名不匹配时应拒绝升级，实际错误: %v", err)
	}
	installed, readErr := os.ReadFile(exePath)
	if readErr != nil || !bytes.Equal(installed, oldBinary) {
		t.Fatal("签名校验失败后不应替换二进制")
	}
}

func TestStatusDisabledSkipsNetwork(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "lactssl")
	if err := os.WriteFile(exePath, []byte{0x7f, 'E', 'L', 'F'}, 0o755); err != nil {
		t.Fatal(err)
	}
	updater := newProgramTestUpdater(t, exePath, dir, "http://127.0.0.1:1", nil)
	updater.enabled = false
	status := updater.Status(context.Background(), true)
	if status.Enabled {
		t.Fatal("状态应显示为已禁用")
	}
	if status.CheckError != "" {
		t.Fatalf("禁用时不应发起网络请求: %s", status.CheckError)
	}
	if status.Reason == "" {
		t.Fatal("禁用时应给出原因")
	}
}

func TestSourceBuildStatusSkipsNetwork(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "lactssl")
	if err := os.WriteFile(exePath, []byte{0x7f, 'E', 'L', 'F'}, 0o755); err != nil {
		t.Fatal(err)
	}
	updater := newProgramTestUpdater(t, exePath, dir, "http://127.0.0.1:1", nil)
	updater.currentVersion = "1.2.0-3-gabc1234"
	status := updater.Status(context.Background(), true)
	if status.CheckError != "" {
		t.Fatalf("源码构建不应发起网络请求: %s", status.CheckError)
	}
	if status.CanUpdate || status.UpdateAvailable {
		t.Fatal("源码构建不应参与在线升级")
	}
	if !strings.Contains(status.Reason, "源码") {
		t.Fatalf("应提示源码构建限制: %s", status.Reason)
	}
}

func newProgramTestUpdater(t *testing.T, exePath, dataDir, apiBase string, st *store.Store) *ProgramUpdater {
	t.Helper()
	updater, err := NewProgramUpdater(ProgramOptions{
		CurrentVersion: "1.0.0",
		Enabled:        true,
		Repo:           "acme/lactssl",
		Channel:        "stable",
		APIBase:        apiBase,
		DataDir:        dataDir,
		ExePath:        exePath,
		GOOS:           "linux",
		GOARCH:         "amd64",
		CheckInterval:  0,
		MaxDownloadMB:  16,
		AllowUnsigned:  true,
		Store:          st,
	})
	if err != nil {
		t.Fatalf("创建程序升级管理器失败: %v", err)
	}
	return updater
}

func newProgramTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "lactssl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return store.New(conn, cipher)
}

func TestSafeReleaseAssetName(t *testing.T) {
	valid := []string{"lactssl_1.2.3_linux_amd64.tar.gz", "lactssl_beta_windows_arm64.zip"}
	for _, name := range valid {
		if got, err := safeReleaseAssetName(name); err != nil || got != name {
			t.Fatalf("合法名称被拒绝: %q -> %q, %v", name, got, err)
		}
	}
	invalid := []string{
		"", ".", "..",
		"../lactssl_1.2.3_linux_amd64.tar.gz",
		"a/b.tar.gz",
		`a\b.tar.gz`,
	}
	for _, name := range invalid {
		if _, err := safeReleaseAssetName(name); err == nil {
			t.Fatalf("不安全名称应被拒绝: %q", name)
		}
	}
}

func buildTarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gz)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func mustParseTime(t *testing.T, value string) (parsed time.Time) {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestApplyRejectsUnsignedByDefault(t *testing.T) {
	updater := &ProgramUpdater{enabled: true}
	if _, err := updater.Apply(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "public_key") {
		t.Fatalf("未配置公钥且未允许未签名升级时应拒绝: %v", err)
	}
}

func TestStatusRequiresSignature(t *testing.T) {
	updater := &ProgramUpdater{enabled: false, currentVersion: "1.0.0", goos: "linux", goarch: "amd64"}
	status := updater.Status(context.Background(), false)
	if status.SignatureRequired {
		t.Fatal("未配置公钥时不应标记为需要签名校验")
	}
	if status.AllowUnsigned {
		t.Fatal("默认不应允许未签名升级")
	}
}
