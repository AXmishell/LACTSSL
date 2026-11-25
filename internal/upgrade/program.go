package upgrade

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ulikunitz/xz"

	"lactssl/internal/outbound"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

const (
	programLastRunSetting   = "upgrade.program.last_run"
	programDefaultRepo      = "AXmishell/LACTSSL"
	programDefaultAPI       = "https://api.github.com"
	programChecksumAsset    = "checksums.txt"
	programChecksumSigAsset = "checksums.txt.sig"
)

// gitDescribePattern 匹配脚本/本地构建的版本后缀，例如 1.2.0-3-gabc1234。
var gitDescribePattern = regexp.MustCompile(`-\d+-g[0-9a-fA-F]+(-dirty)?$`)

// ProgramOptions 构造程序自升级所需的依赖。
type ProgramOptions struct {
	// CurrentVersion 是当前二进制版本，由 ldflags 注入。
	CurrentVersion string
	// Enabled 表示允许在线检查与安装新版本。
	Enabled bool
	// Repo 是 GitHub 仓库，格式为 owner/repo。
	Repo string
	// Channel 为 stable 或 beta。
	Channel string
	// APIBase 默认使用 GitHub API，测试或多镜像场景可覆盖。
	APIBase string
	// DataDir 用于保存升级备份与下载临时文件。
	DataDir string
	// ExePath 可覆盖当前可执行文件路径，测试时指向临时文件。
	ExePath string
	// GOOS/GOARCH 可覆盖运行平台，测试时模拟其他架构。
	GOOS   string
	GOARCH string
	// CheckInterval 是自动检查新版本的最小间隔。
	CheckInterval time.Duration
	// MaxDownloadMB 是发布包下载大小上限。
	MaxDownloadMB int
	// Token 是可选的 GitHub API token，用于提高速率限制。
	Token string
	// PublicKey 是发布校验和签名（checksums.txt.sig）的 Ed25519 公钥（base64）。
	// 配置后强制验签，防止 Release 资产与 checksums.txt 被同时替换。
	PublicKey string
	// AllowUnsigned 表示在未配置 PublicKey 时仍允许安装未签名发布包。
	// 默认 false：未配置公钥时拒绝 Apply。
	AllowUnsigned bool
	// HTTP 可注入测试客户端；为空时使用带 SSRF 防护的默认客户端。
	HTTP   *http.Client
	Store  *store.Store
	Logger *slog.Logger
}

// ErrSignatureRequired 表示未配置发布公钥且未允许未签名升级。
var ErrSignatureRequired = errors.New("未配置 upgrade.program.public_key：已拒绝未签名升级；请配置发布公钥，或在受控环境显式设置 allow_unsigned")

// ProgramUpdater 负责检查 GitHub Release、下载校验并替换当前二进制。
type ProgramUpdater struct {
	currentVersion string
	enabled        bool
	repo           string
	channel        string
	apiBase        string
	dataDir        string
	exePath        string
	goos           string
	goarch         string
	checkInterval  time.Duration
	maxDownloadMB  int
	token          string
	publicKey      ed25519.PublicKey
	allowUnsigned  bool
	http           *http.Client
	downloadHTTP   *http.Client
	store          *store.Store
	logger         *slog.Logger

	mu             sync.Mutex
	release        *ProgramRelease
	checkedAt      time.Time
	checkErr       string
	pendingRestart bool

	// applyMu 串行化安装，避免并发下载/替换二进制。
	applyMu sync.Mutex
}

// ProgramAsset 是当前平台对应的发布包。
type ProgramAsset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

// ProgramRelease 是已解析的 GitHub Release 信息。
type ProgramRelease struct {
	Version     string       `json:"version"`
	Tag         string       `json:"tag"`
	Name        string       `json:"name,omitempty"`
	Notes       string       `json:"notes,omitempty"`
	URL         string       `json:"url,omitempty"`
	PublishedAt time.Time    `json:"published_at"`
	Prerelease  bool         `json:"prerelease"`
	Asset       ProgramAsset `json:"asset"`
	Checksum    ProgramAsset `json:"checksum"`
	// ChecksumSig 是 checksums.txt.sig（base64 的 Ed25519 签名）。
	// 配置了 public_key 时必须存在且验签通过。
	ChecksumSig ProgramAsset `json:"checksum_sig,omitempty"`
}

// ProgramRunReport 是一次程序升级的完整结果，同时持久化到 upgrade.program.last_run。
type ProgramRunReport struct {
	FromVersion string    `json:"from_version"`
	ToVersion   string    `json:"to_version"`
	Asset       string    `json:"asset,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
	BackupPath  string    `json:"backup_path,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	// RestartRequired 表示二进制已替换，需要重启进程后生效。
	RestartRequired bool `json:"restart_required"`
	// RestartMode 为 exec（进程内重新执行）或 exit（依赖服务管理器拉起）。
	RestartMode string `json:"restart_mode,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ProgramStatus 是 /upgrade 页面程序升级卡片的只读状态。
type ProgramStatus struct {
	Enabled         bool              `json:"enabled"`
	Repo            string            `json:"repo"`
	Channel         string            `json:"channel"`
	CurrentVersion  string            `json:"current_version"`
	Platform        string            `json:"platform"`
	Latest          *ProgramRelease   `json:"latest,omitempty"`
	UpdateAvailable bool              `json:"update_available"`
	CanUpdate       bool              `json:"can_update"`
	Reason          string            `json:"reason,omitempty"`
	RestartMode     string            `json:"restart_mode"`
	CheckedAt       time.Time         `json:"checked_at"`
	CheckError      string            `json:"check_error,omitempty"`
	LastRun         *ProgramRunReport `json:"last_run,omitempty"`
	// RestartPending 表示二进制已替换但进程仍在运行旧版本。
	RestartPending bool `json:"restart_pending"`
	// SignatureRequired 表示当前配置要求校验发布签名。
	SignatureRequired bool `json:"signature_required"`
	// AllowUnsigned 表示管理员显式允许未签名升级。
	AllowUnsigned bool `json:"allow_unsigned"`
}

// NewProgramUpdater 创建程序升级管理器。配置不完整时返回错误，避免运行期才暴露。
func NewProgramUpdater(opts ProgramOptions) (*ProgramUpdater, error) {
	repo := strings.TrimSpace(opts.Repo)
	if repo == "" {
		repo = programDefaultRepo
	}
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("程序升级仓库必须是 owner/repo 格式")
	}
	channel := strings.ToLower(strings.TrimSpace(opts.Channel))
	if channel == "" {
		channel = "stable"
	}
	if channel != "stable" && channel != "beta" {
		return nil, fmt.Errorf("程序升级通道必须是 stable 或 beta")
	}
	goos := strings.TrimSpace(opts.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := strings.TrimSpace(opts.GOARCH)
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	exe := strings.TrimSpace(opts.ExePath)
	if exe == "" {
		resolved, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("无法定位当前可执行文件: %w", err)
		}
		exe = resolved
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		// 替换真实文件而不是符号链接，保留部署目录中的版本链接结构。
		exe = resolved
	}
	publicKey, err := parseProgramPublicKey(opts.PublicKey)
	if err != nil {
		return nil, err
	}
	interval := opts.CheckInterval
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	maxDownload := opts.MaxDownloadMB
	if maxDownload <= 0 {
		maxDownload = 128
	}
	httpClient := opts.HTTP
	downloadClient := opts.HTTP
	if httpClient == nil {
		// API 与校验和响应很小，保持较短超时，避免页面检查长时间挂起。
		httpClient = outbound.NewClient(30*time.Second, false)
		// 发布包约数十 MB，慢速网络下 60 秒不够；仍受 HTTP 请求上下文与
		// 服务端 10 分钟写超时约束，避免无限挂起。
		downloadClient = outbound.NewClient(8*time.Minute, false)
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	apiBase := strings.TrimRight(strings.TrimSpace(opts.APIBase), "/")
	if apiBase == "" {
		apiBase = programDefaultAPI
	}
	return &ProgramUpdater{
		currentVersion: strings.TrimSpace(opts.CurrentVersion),
		enabled:        opts.Enabled,
		repo:           repo,
		channel:        channel,
		apiBase:        apiBase,
		dataDir:        strings.TrimSpace(opts.DataDir),
		exePath:        exe,
		goos:           goos,
		goarch:         goarch,
		checkInterval:  interval,
		maxDownloadMB:  maxDownload,
		token:          strings.TrimSpace(opts.Token),
		publicKey:      publicKey,
		allowUnsigned:  opts.AllowUnsigned,
		http:           httpClient,
		downloadHTTP:   downloadClient,
		store:          opts.Store,
		logger:         logger,
	}, nil
}

// Status 返回程序升级状态。force 为 true 时忽略检查间隔立即请求 GitHub。
func (u *ProgramUpdater) Status(ctx context.Context, force bool) ProgramStatus {
	status := ProgramStatus{
		Enabled:           u.enabled,
		Repo:              u.repo,
		Channel:           u.channel,
		CurrentVersion:    u.currentVersion,
		Platform:          u.goos + "/" + u.goarch,
		RestartMode:       u.restartMode(),
		SignatureRequired: u.publicKey != nil,
		AllowUnsigned:     u.allowUnsigned,
	}

	u.mu.Lock()
	// 源码/开发构建不参与在线升级，也无需为它请求 GitHub。
	needCheck := u.enabled && !isSourceBuildVersion(u.currentVersion) &&
		(force || u.release == nil || time.Since(u.checkedAt) >= u.checkInterval)
	if needCheck {
		release, err := u.fetchLatest(ctx)
		u.checkedAt = time.Now().UTC()
		if err != nil {
			u.checkErr = err.Error()
		} else {
			u.release = &release
			u.checkErr = ""
		}
	}
	release := u.release
	checkedAt := u.checkedAt
	checkErr := u.checkErr
	u.mu.Unlock()

	status.CheckedAt = checkedAt
	status.CheckError = checkErr
	status.Latest = release
	status.LastRun = u.lastRun()
	u.mu.Lock()
	pendingRestart := u.pendingRestart
	u.mu.Unlock()
	if pendingRestart || (status.LastRun != nil && status.LastRun.RestartRequired && status.LastRun.Error == "" &&
		compareProgramVersion(status.LastRun.ToVersion, u.currentVersion) > 0) {
		status.RestartPending = true
	}

	if !u.enabled {
		status.Reason = "程序自升级已在配置中关闭（upgrade.program.enabled=false）"
		return status
	}
	if isSourceBuildVersion(u.currentVersion) {
		status.Reason = "当前是源码/开发构建，不参与在线升级；请使用正式发布版本"
		return status
	}
	if release == nil {
		if checkErr == "" {
			status.Reason = "尚未获取版本信息"
		} else {
			status.Reason = "检查新版本失败: " + checkErr
		}
		return status
	}
	if compareProgramVersion(release.Version, u.currentVersion) > 0 {
		status.UpdateAvailable = true
	}
	if !status.UpdateAvailable {
		status.Reason = "当前已是最新版本"
		return status
	}
	if release.Asset.Name == "" {
		status.Reason = fmt.Sprintf("v%s 没有适用于 %s/%s 的发布包", release.Version, u.goos, u.goarch)
		return status
	}
	if _, _, ok := parseProgramVersion(u.currentVersion); !ok {
		status.Reason = "当前版本不是正式语义化版本，无法自动升级；请手动替换二进制"
		return status
	}
	if _, err := u.capability(); err != nil {
		status.Reason = err.Error()
		return status
	}
	if u.publicKey == nil && !u.allowUnsigned {
		status.Reason = "未配置 upgrade.program.public_key，已拒绝在线升级；请配置发布公钥，或在受控环境显式设置 allow_unsigned"
		return status
	}
	status.CanUpdate = true
	return status
}

// Apply 下载指定版本（version 为空表示最新版）、校验并替换当前二进制。
func (u *ProgramUpdater) Apply(ctx context.Context, version string) (*ProgramRunReport, error) {
	u.applyMu.Lock()
	defer u.applyMu.Unlock()
	if u.publicKey == nil && !u.allowUnsigned {
		return nil, ErrSignatureRequired
	}

	report := &ProgramRunReport{
		FromVersion: u.currentVersion,
		StartedAt:   time.Now().UTC(),
		RestartMode: u.restartMode(),
	}
	fail := func(err error) (*ProgramRunReport, error) {
		report.FinishedAt = time.Now().UTC()
		report.Error = err.Error()
		u.saveLastRun(report)
		return report, err
	}

	if !u.enabled {
		return fail(errors.New("程序自升级已在配置中关闭"))
	}
	if u.RestartPending() {
		return fail(errors.New("新版本已安装，请先重启服务"))
	}
	if _, err := u.capability(); err != nil {
		return fail(err)
	}

	release, err := u.fetchLatest(ctx)
	if err != nil {
		return fail(err)
	}
	u.mu.Lock()
	u.release = &release
	u.checkedAt = time.Now().UTC()
	u.checkErr = ""
	u.mu.Unlock()

	report.ToVersion = release.Version
	report.Asset = release.Asset.Name
	if compareProgramVersion(release.Version, u.currentVersion) <= 0 {
		return fail(fmt.Errorf("当前版本 %s 不低于最新版本 %s", u.currentVersion, release.Version))
	}
	if strings.TrimSpace(version) != "" {
		requested := normalizeProgramVersion(version)
		if requested != release.Version {
			return fail(fmt.Errorf("目标版本 %s 与最新版本 %s 不一致，请重新检查", version, release.Version))
		}
	}
	if release.Asset.Name == "" {
		return fail(fmt.Errorf("v%s 没有适用于 %s/%s 的发布包", release.Version, u.goos, u.goarch))
	}
	if release.Asset.Size > int64(u.maxDownloadMB)<<20 {
		return fail(fmt.Errorf("发布包大小 %d 字节超过 %d MB 限制", release.Asset.Size, u.maxDownloadMB))
	}

	workDir, err := os.MkdirTemp(u.dataDir, "program-update-*")
	if err != nil {
		return fail(fmt.Errorf("创建升级临时目录失败: %w", err))
	}
	defer os.RemoveAll(workDir)
	if err := os.Chmod(workDir, 0o700); err != nil {
		return fail(fmt.Errorf("设置升级临时目录权限失败: %w", err))
	}

	expected, err := u.resolveChecksum(ctx, release)
	if err != nil {
		return fail(err)
	}
	report.SHA256 = expected

	assetName, err := safeReleaseAssetName(release.Asset.Name)
	if err != nil {
		return fail(err)
	}
	archivePath := filepath.Join(workDir, assetName)
	size, err := u.download(ctx, release.Asset.URL, archivePath, int64(u.maxDownloadMB)<<20)
	if err != nil {
		return fail(fmt.Errorf("下载发布包失败: %w", err))
	}
	if release.Asset.Size > 0 && size != release.Asset.Size {
		return fail(fmt.Errorf("发布包大小不符：期望 %d，实际 %d", release.Asset.Size, size))
	}
	digest, err := util.FileSHA256(archivePath)
	if err != nil {
		return fail(err)
	}
	if !strings.EqualFold(digest, expected) {
		return fail(fmt.Errorf("发布包 sha256 校验失败：期望 %s，实际 %s", expected, digest))
	}

	exeDir := filepath.Dir(u.exePath)
	newBinary, err := os.CreateTemp(exeDir, ".lactssl-update-*")
	if err != nil {
		return fail(fmt.Errorf("在程序目录创建临时文件失败: %w", err))
	}
	newPath := newBinary.Name()
	defer os.Remove(newPath)
	if err := extractProgramBinary(archivePath, u.goos, newBinary, int64(u.maxDownloadMB)<<20); err != nil {
		_ = newBinary.Close()
		return fail(err)
	}
	if err := newBinary.Sync(); err != nil {
		_ = newBinary.Close()
		return fail(fmt.Errorf("写入新程序失败: %w", err))
	}
	if err := newBinary.Close(); err != nil {
		return fail(fmt.Errorf("写入新程序失败: %w", err))
	}
	// 沿用旧二进制的权限位，避免依赖特定部署时的 0750 等设置被重置。
	mode := os.FileMode(0o755)
	if info, statErr := os.Stat(u.exePath); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(newPath, mode); err != nil {
		return fail(fmt.Errorf("设置新程序权限失败: %w", err))
	}
	if err := validateProgramBinary(u.goos, newPath); err != nil {
		return fail(err)
	}

	backupPath, err := u.backupProgram(release.Version)
	if err != nil {
		return fail(err)
	}
	report.BackupPath = backupPath

	if err := replaceProgramBinary(u.exePath, newPath); err != nil {
		return fail(fmt.Errorf("替换程序失败: %w", err))
	}
	report.RestartRequired = true
	report.FinishedAt = time.Now().UTC()
	u.mu.Lock()
	u.pendingRestart = true
	u.mu.Unlock()
	u.saveLastRun(report)
	u.logger.Info("程序升级完成，等待重启",
		"from_version", report.FromVersion,
		"to_version", report.ToVersion,
		"asset", report.Asset,
		"backup_path", report.BackupPath)
	return report, nil
}

// capability 检查当前部署是否允许原地替换二进制，并返回重启方式。
func (u *ProgramUpdater) capability() (string, error) {
	if isSourceBuildVersion(u.currentVersion) {
		return u.restartMode(), errors.New("当前是源码/开发构建，不参与在线升级；请使用正式发布版本")
	}
	if _, _, ok := parseProgramVersion(u.currentVersion); !ok {
		return u.restartMode(), errors.New("当前版本不是正式语义化版本，无法自动升级；请手动替换二进制")
	}
	info, err := os.Stat(u.exePath)
	if err != nil {
		return u.restartMode(), fmt.Errorf("无法读取当前程序文件: %w", err)
	}
	if info.IsDir() {
		return u.restartMode(), errors.New("当前程序路径是目录，无法自动升级")
	}
	probe, err := os.CreateTemp(filepath.Dir(u.exePath), ".lactssl-probe-*")
	if err != nil {
		return u.restartMode(), fmt.Errorf("程序所在目录不可写，无法自动升级（只读容器或受保护目录）: %w", err)
	}
	probePath := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probePath)
	return u.restartMode(), nil
}

func (u *ProgramUpdater) restartMode() string {
	if runtime.GOOS == "windows" {
		return "exit"
	}
	return "exec"
}

// fetchLatest 查询 GitHub Release。beta 通道会选择包含预发布在内的最高版本。
func (u *ProgramUpdater) fetchLatest(ctx context.Context) (ProgramRelease, error) {
	base := fmt.Sprintf("%s/repos/%s/releases", u.apiBase, u.repo)
	endpoint := base + "/latest"
	if u.channel == "beta" {
		endpoint = base + "?per_page=30"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ProgramRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "LACTSSL/"+strings.TrimSpace(u.currentVersion))
	if u.token != "" {
		request.Header.Set("Authorization", "Bearer "+u.token)
	}
	response, err := u.http.Do(request)
	if err != nil {
		return ProgramRelease{}, fmt.Errorf("请求 GitHub Release 失败: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return ProgramRelease{}, fmt.Errorf("读取 GitHub Release 失败: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return ProgramRelease{}, fmt.Errorf("GitHub API 返回 HTTP %d: %s", response.StatusCode, githubErrorText(raw))
	}

	var releases []ghRelease
	if u.channel == "beta" {
		if err := json.Unmarshal(raw, &releases); err != nil {
			return ProgramRelease{}, fmt.Errorf("解析 GitHub Release 失败: %w", err)
		}
	} else {
		var single ghRelease
		if err := json.Unmarshal(raw, &single); err != nil {
			return ProgramRelease{}, fmt.Errorf("解析 GitHub Release 失败: %w", err)
		}
		releases = []ghRelease{single}
	}

	var picked *ghRelease
	for index := range releases {
		item := &releases[index]
		if item.Draft || strings.TrimSpace(item.TagName) == "" {
			continue
		}
		if u.channel == "stable" && item.Prerelease {
			continue
		}
		if picked == nil || compareProgramVersion(normalizeProgramVersion(item.TagName), normalizeProgramVersion(picked.TagName)) > 0 {
			picked = item
		}
	}
	if picked == nil {
		return ProgramRelease{}, errors.New("GitHub 仓库没有可用的正式发布版本")
	}

	version := normalizeProgramVersion(picked.TagName)
	release := ProgramRelease{
		Version:     version,
		Tag:         picked.TagName,
		Name:        picked.Name,
		Notes:       truncateText(picked.Body, 32<<10),
		URL:         picked.HTMLURL,
		PublishedAt: picked.PublishedAt,
		Prerelease:  picked.Prerelease,
	}
	if asset, ok := selectProgramAsset(picked.Assets, version, u.goos, u.goarch); ok {
		release.Asset = ProgramAsset{
			Name:   asset.Name,
			URL:    asset.BrowserDownloadURL,
			Size:   asset.Size,
			SHA256: normalizeGitHubDigest(asset.Digest),
		}
	}
	if checksum, ok := findReleaseAsset(picked.Assets, programChecksumAsset); ok {
		release.Checksum = ProgramAsset{
			Name: checksum.Name,
			URL:  checksum.BrowserDownloadURL,
			Size: checksum.Size,
		}
	}
	if signature, ok := findReleaseAsset(picked.Assets, programChecksumSigAsset); ok {
		release.ChecksumSig = ProgramAsset{
			Name: signature.Name,
			URL:  signature.BrowserDownloadURL,
			Size: signature.Size,
		}
	}
	return release, nil
}

// resolveChecksum 读取 checksums.txt。配置了公钥时必须先验证 checksums.txt.sig，
// 未配置公钥时保持旧的 HTTPS + SHA-256 信任模型。
func (u *ProgramUpdater) resolveChecksum(ctx context.Context, release ProgramRelease) (string, error) {
	if u.publicKey != nil {
		if release.Checksum.URL == "" {
			return "", errors.New("已配置发布签名公钥，但 release 缺少 checksums.txt，已拒绝升级")
		}
		raw, err := u.fetchSmall(ctx, release.Checksum.URL, 1<<20)
		if err != nil {
			return "", fmt.Errorf("读取校验和失败: %w", err)
		}
		if err := u.verifyChecksumSignature(ctx, release, raw); err != nil {
			return "", err
		}
		digest, ok := checksumForAsset(raw, release.Asset.Name)
		if !ok {
			return "", fmt.Errorf("checksums.txt 中缺少 %s 的摘要", release.Asset.Name)
		}
		return digest, nil
	}
	if release.Checksum.URL != "" {
		raw, err := u.fetchSmall(ctx, release.Checksum.URL, 1<<20)
		if err != nil {
			return "", fmt.Errorf("读取校验和失败: %w", err)
		}
		digest, ok := checksumForAsset(raw, release.Asset.Name)
		if ok {
			return digest, nil
		}
	}
	if value := normalizeGitHubDigest(release.Asset.SHA256); value != "" {
		return value, nil
	}
	return "", errors.New("发布包缺少 checksums.txt 或 sha256，已拒绝升级")
}

// verifyChecksumSignature 校验 checksums.txt 的 Ed25519 分离签名。
func (u *ProgramUpdater) verifyChecksumSignature(ctx context.Context, release ProgramRelease, raw []byte) error {
	sigURL := release.ChecksumSig.URL
	if sigURL == "" && release.Checksum.URL != "" {
		sigURL = release.Checksum.URL + ".sig"
	}
	if sigURL == "" {
		return errors.New("发布包缺少 checksums.txt.sig，已拒绝升级")
	}
	encoded, err := u.fetchSmall(ctx, sigURL, 4096)
	if err != nil {
		return fmt.Errorf("读取发布签名失败: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		signature, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	}
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("发布签名格式不正确")
	}
	if !ed25519.Verify(u.publicKey, raw, signature) {
		return errors.New("发布签名校验失败，已拒绝升级")
	}
	return nil
}

// parseProgramPublicKey 解析 base64 编码的 Ed25519 公钥；空值表示不启用验签。
func parseProgramPublicKey(value string) (ed25519.PublicKey, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("upgrade.program.public_key 必须是 base64 编码的 Ed25519 公钥")
	}
	return ed25519.PublicKey(raw), nil
}

func (u *ProgramUpdater) fetchSmall(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("不支持的下载协议: %s", parsed.Scheme)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "LACTSSL/"+strings.TrimSpace(u.currentVersion))
	if u.token != "" && strings.HasPrefix(rawURL, "https://api.github.com/") {
		request.Header.Set("Authorization", "Bearer "+u.token)
	}
	response, err := u.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("服务器返回 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("响应超过大小限制")
	}
	return raw, nil
}

// download 把发布包写入 dest，并返回实际写入的字节数。
func (u *ProgramUpdater) download(ctx context.Context, rawURL, dest string, limit int64) (int64, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return 0, fmt.Errorf("不支持的下载协议: %s", parsed.Scheme)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("User-Agent", "LACTSSL/"+strings.TrimSpace(u.currentVersion))
	response, err := u.downloadHTTP.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("服务器返回 HTTP %d", response.StatusCode)
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	success := false
	defer func() {
		_ = out.Close()
		if !success {
			_ = os.Remove(dest)
		}
	}()
	written, err := io.Copy(out, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return 0, err
	}
	if written > limit {
		return 0, fmt.Errorf("发布包超过 %d MB 限制", u.maxDownloadMB)
	}
	if err := out.Sync(); err != nil {
		return 0, err
	}
	success = true
	return written, nil
}

// backupProgram 把当前二进制复制到数据目录，便于失败时手工回滚。
func (u *ProgramUpdater) backupProgram(toVersion string) (string, error) {
	if u.dataDir == "" {
		return "", nil
	}
	dir := filepath.Join(u.dataDir, "upgrade-backups", "program")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建程序备份目录失败: %w", err)
	}
	name := fmt.Sprintf("%s-lactssl-%s%s", time.Now().UTC().Format("20060102-150405"), sanitizeProgramName(u.currentVersion), filepath.Ext(u.exePath))
	target := filepath.Join(dir, name)
	if err := copyProgramFile(u.exePath, target, 0o700); err != nil {
		return "", fmt.Errorf("备份当前程序失败: %w", err)
	}
	pruneProgramBackups(dir, 3)
	return target, nil
}

// ExecutablePath 返回被替换的真实二进制路径。重启时不能重新调用
// os.Executable，因为旧路径在替换后可能显示为 " (deleted)"。
func (u *ProgramUpdater) ExecutablePath() string { return u.exePath }

// RestartPending 表示二进制已替换但当前进程仍在运行旧版本。
func (u *ProgramUpdater) RestartPending() bool {
	u.mu.Lock()
	pending := u.pendingRestart
	u.mu.Unlock()
	if pending {
		return true
	}
	last := u.lastRun()
	if last == nil || !last.RestartRequired || last.Error != "" {
		return false
	}
	return compareProgramVersion(last.ToVersion, u.currentVersion) > 0
}

func (u *ProgramUpdater) lastRun() *ProgramRunReport {
	if u.store == nil {
		return nil
	}
	raw, err := u.store.GetSetting(programLastRunSetting)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	var report ProgramRunReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil
	}
	return &report
}

func (u *ProgramUpdater) saveLastRun(report *ProgramRunReport) {
	if u.store == nil {
		return
	}
	data, err := json.Marshal(report)
	if err != nil {
		return
	}
	if err := u.store.SetSetting(programLastRunSetting, string(data)); err != nil {
		u.logger.Warn("保存程序升级记录失败", "error", err)
	}
}

// ghRelease 是 GitHub Release API 的子集。
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

// safeReleaseAssetName 把远端 Release asset 名称限制为单一文件名。
// 名称来自外部 API，不能直接用于 filepath.Join。
func safeReleaseAssetName(name string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	base := filepath.Base(normalized)
	if base == "" || base == "." || base == ".." || base != name || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("发布包名称不安全: %s", name)
	}
	return base, nil
}

func findReleaseAsset(assets []ghAsset, name string) (ghAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return ghAsset{}, false
}

// selectProgramAsset 按发布脚本约定选择当前平台的压缩包。
// 优先选择 xz（下载体积更小），并保留 gz 以兼容旧版本客户端。
func selectProgramAsset(assets []ghAsset, version, goos, goarch string) (ghAsset, bool) {
	prefix := fmt.Sprintf("lactssl_%s_%s_%s", version, goos, goarch)
	if goos == "windows" {
		if asset, ok := findReleaseAsset(assets, prefix+".zip"); ok {
			return asset, true
		}
	} else {
		for _, suffix := range []string{".tar.xz", ".txz", ".tar.gz", ".tgz"} {
			if asset, ok := findReleaseAsset(assets, prefix+suffix); ok {
				return asset, true
			}
		}
	}
	// 兼容手工上传的发布包：只要求名称包含平台标记且扩展名正确。
	suffixes := []string{".tar.xz", ".txz", ".tar.gz", ".tgz"}
	if goos == "windows" {
		suffixes = []string{".zip"}
	}
	for _, suffix := range suffixes {
		for _, asset := range assets {
			name := asset.Name
			if !strings.HasPrefix(name, "lactssl_") || !strings.Contains(name, "_"+goos+"_"+goarch) {
				continue
			}
			if strings.HasSuffix(name, suffix) {
				return asset, true
			}
		}
	}
	return ghAsset{}, false
}

// extractProgramBinary 从 tar.gz / tar.xz / zip 中解出 lactssl（Windows 为 lactssl.exe）。
func extractProgramBinary(archivePath, goos string, out *os.File, limit int64) error {
	want := "lactssl"
	if goos == "windows" {
		want += ".exe"
	}
	if goos == "windows" {
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			return fmt.Errorf("打开发布包失败: %w", err)
		}
		defer reader.Close()
		for _, entry := range reader.File {
			if entry.FileInfo().IsDir() || filepath.Base(entry.Name) != want {
				continue
			}
			src, err := entry.Open()
			if err != nil {
				return fmt.Errorf("读取发布包失败: %w", err)
			}
			err = copyLimited(out, src, limit)
			_ = src.Close()
			if err != nil {
				return err
			}
			return nil
		}
		return fmt.Errorf("发布包中没有 %s", want)
	}

	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("打开发布包失败: %w", err)
	}
	defer file.Close()

	decompressed, closeReader, err := openProgramArchive(file, archivePath)
	if err != nil {
		return err
	}
	defer closeReader()

	reader := tar.NewReader(decompressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("读取发布包失败: %w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != want {
			continue
		}
		return copyLimited(out, reader, limit)
	}
	return fmt.Errorf("发布包中没有 %s", want)
}

// openProgramArchive 根据扩展名返回解压后的 tar 流。
// xz 使用纯 Go 解码器，并对字典大小设上限，避免恶意压缩包占用过多内存。
func openProgramArchive(file *os.File, name string) (io.Reader, func(), error) {
	switch {
	case strings.HasSuffix(name, ".tar.xz"), strings.HasSuffix(name, ".txz"):
		reader, err := (xz.ReaderConfig{DictCap: 64 << 20, SingleStream: true}).NewReader(file)
		if err != nil {
			return nil, func() {}, fmt.Errorf("解压发布包失败: %w", err)
		}
		return reader, func() {}, nil
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		gz, err := gzip.NewReader(file)
		if err != nil {
			return nil, func() {}, fmt.Errorf("解压发布包失败: %w", err)
		}
		return gz, func() { _ = gz.Close() }, nil
	default:
		return nil, func() {}, fmt.Errorf("不支持的发布包格式: %s", filepath.Base(name))
	}
}

func copyLimited(dst io.Writer, src io.Reader, limit int64) error {
	written, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if err != nil {
		return fmt.Errorf("写入新程序失败: %w", err)
	}
	if written > limit {
		return errors.New("新程序超过大小限制")
	}
	if written == 0 {
		return errors.New("发布包中的程序为空")
	}
	return nil
}

// validateProgramBinary 检查文件头，避免把错误平台的产物安装到当前程序。
func validateProgramBinary(goos, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 4)
	if _, err := io.ReadFull(file, header); err != nil {
		return fmt.Errorf("读取新程序文件头失败: %w", err)
	}
	switch goos {
	case "windows":
		if header[0] != 'M' || header[1] != 'Z' {
			return errors.New("发布包中的程序不是 Windows 可执行文件")
		}
	case "darwin":
		macho := (header[0] == 0xcf && header[1] == 0xfa && header[2] == 0xed && header[3] == 0xfe) ||
			(header[0] == 0xfe && header[1] == 0xed && header[2] == 0xfa && header[3] == 0xcf) ||
			(header[0] == 0xca && header[1] == 0xfe && header[2] == 0xba && header[3] == 0xbe)
		if !macho {
			return errors.New("发布包中的程序不是 macOS 可执行文件")
		}
	default:
		if header[0] != 0x7f || header[1] != 'E' || header[2] != 'L' || header[3] != 'F' {
			return errors.New("发布包中的程序不是当前平台的可执行文件")
		}
	}
	return nil
}

// replaceProgramBinary 原子替换二进制。Windows 不允许覆盖运行中的文件，先重命名旧文件。
func replaceProgramBinary(exePath, newPath string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(newPath, exePath)
	}
	oldPath := exePath + ".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(exePath, oldPath); err != nil {
		return err
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Rename(oldPath, exePath)
		return err
	}
	return nil
}

func copyProgramFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		_ = out.Close()
		if !success {
			_ = os.Remove(dst)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	success = true
	return nil
}

func pruneProgramBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= keep {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for index := 0; index < len(names)-keep; index++ {
		_ = os.Remove(filepath.Join(dir, names[index]))
	}
}

func checksumForAsset(raw []byte, name string) (string, bool) {
	want := strings.TrimSpace(name)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		digest, err := normalizeProgramDigest(fields[0])
		if err != nil {
			continue
		}
		file := strings.TrimPrefix(fields[len(fields)-1], "*")
		file = strings.TrimPrefix(file, "./")
		if file == want || filepath.Base(file) == want {
			return digest, true
		}
	}
	return "", false
}

func normalizeProgramDigest(value string) (string, error) {
	digest := strings.ToLower(strings.TrimSpace(value))
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) != sha256.Size*2 {
		return "", errors.New("sha256 格式不正确")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", errors.New("sha256 格式不正确")
	}
	return digest, nil
}

func normalizeGitHubDigest(value string) string {
	digest, err := normalizeProgramDigest(value)
	if err != nil {
		return ""
	}
	return digest
}

func normalizeProgramVersion(value string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
}

// isSourceBuildVersion 判断版本是否来自 dev 或 git describe 的源码构建。
// 这类构建可能包含发布包之后的提交，不能用 Release 反向覆盖。
func isSourceBuildVersion(value string) bool {
	version := normalizeProgramVersion(value)
	if version == "" || version == "dev" {
		return true
	}
	return strings.HasSuffix(version, "-dirty") || gitDescribePattern.MatchString(version)
}

// parseProgramVersion 解析语义化版本；git describe 后缀会被视为预发布。
func parseProgramVersion(value string) ([3]int, string, bool) {
	text := normalizeProgramVersion(value)
	var out [3]int
	if text == "" || text == "dev" {
		return out, "", false
	}
	// 构建元数据不参与比较。
	if index := strings.Index(text, "+"); index >= 0 {
		text = text[:index]
	}
	pre := ""
	if index := strings.Index(text, "-"); index >= 0 {
		pre = text[index+1:]
		text = text[:index]
	}
	parts := strings.Split(text, ".")
	if len(parts) < 2 {
		return out, "", false
	}
	for index := 0; index < 3; index++ {
		if index >= len(parts) {
			out[index] = 0
			continue
		}
		number, err := strconv.Atoi(parts[index])
		if err != nil || number < 0 {
			return out, "", false
		}
		out[index] = number
	}
	return out, pre, true
}

// compareProgramVersion 比较两个版本：数字段优先，正式版高于同版本预发布。
func compareProgramVersion(a, b string) int {
	coreA, preA, okA := parseProgramVersion(a)
	coreB, preB, okB := parseProgramVersion(b)
	if !okA || !okB {
		return 0
	}
	for index := 0; index < 3; index++ {
		if coreA[index] > coreB[index] {
			return 1
		}
		if coreA[index] < coreB[index] {
			return -1
		}
	}
	if preA == "" && preB == "" {
		return 0
	}
	if preA == "" {
		return 1
	}
	if preB == "" {
		return -1
	}
	return comparePrerelease(preA, preB)
}

func comparePrerelease(a, b string) int {
	partsA := strings.FieldsFunc(a, func(r rune) bool { return r == '.' || r == '-' })
	partsB := strings.FieldsFunc(b, func(r rune) bool { return r == '.' || r == '-' })
	for index := 0; index < len(partsA) && index < len(partsB); index++ {
		left, right := partsA[index], partsB[index]
		leftNumber, leftErr := strconv.Atoi(left)
		rightNumber, rightErr := strconv.Atoi(right)
		switch {
		case leftErr == nil && rightErr == nil:
			if leftNumber != rightNumber {
				if leftNumber > rightNumber {
					return 1
				}
				return -1
			}
		case leftErr == nil:
			return -1
		case rightErr == nil:
			return 1
		default:
			if compared := strings.Compare(left, right); compared != 0 {
				return compared
			}
		}
	}
	if len(partsA) != len(partsB) {
		if len(partsA) > len(partsB) {
			return 1
		}
		return -1
	}
	return 0
}

func sanitizeProgramName(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, strings.TrimSpace(value))
	cleaned = strings.Trim(cleaned, "-")
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}

func truncateText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n…"
}

func githubErrorText(raw []byte) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && strings.TrimSpace(payload.Message) != "" {
		return payload.Message
	}
	text := strings.TrimSpace(string(raw))
	if len(text) > 200 {
		text = text[:200]
	}
	if text == "" {
		return "未知错误"
	}
	return text
}
