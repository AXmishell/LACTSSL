// Package pluginrepo 实现插件仓库索引、下载、校验与解包。
package pluginrepo

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/outbound"
)

const (
	// SchemaVersion 是支持的索引 schema。
	SchemaVersion = 1
	// ProtocolVersion 是支持的插件协议版本。
	ProtocolVersion = "v1"
	maxIndexBytes   = 8 << 20
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Asset 是某个平台上的插件包。
type Asset struct {
	URL              string `json:"url"`
	SHA256           string `json:"sha256"`
	Size             int64  `json:"size"`
	UncompressedSize int64  `json:"uncompressed_size,omitempty"`
}

// Plugin 是仓库中的一个插件版本。
type Plugin struct {
	Kind             string           `json:"kind"`
	ID               string           `json:"id"`
	Name             string           `json:"name,omitempty"`
	Description      string           `json:"description,omitempty"`
	Version          string           `json:"version"`
	Protocol         string           `json:"protocol"`
	MinHost          string           `json:"min_host,omitempty"`
	Bundle           bool             `json:"bundle,omitempty"`
	Providers        []string         `json:"providers,omitempty"`
	HideIcon         bool             `json:"hide_icon,omitempty"`
	UncompressedSize int64            `json:"uncompressed_size,omitempty"`
	Single           bool             `json:"single,omitempty"`
	Deprecated       bool             `json:"deprecated,omitempty"`
	Assets           map[string]Asset `json:"assets"`
}

// Index 是插件仓库索引。
type Index struct {
	Schema      int      `json:"schema"`
	Channel     string   `json:"channel"`
	Protocol    string   `json:"protocol"`
	GeneratedAt string   `json:"generated_at"`
	Plugins     []Plugin `json:"plugins"`
	Icons       []Icon   `json:"icons,omitempty"`
}

// Metadata 是插件包内的 plugin.json。
type Metadata struct {
	Schema     int                 `json:"schema"`
	Kind       string              `json:"kind"`
	ID         string              `json:"id"`
	Version    string              `json:"version"`
	Protocol   string              `json:"protocol"`
	OS         string              `json:"os"`
	Arch       string              `json:"arch"`
	Entrypoint string              `json:"entrypoint"`
	Bundle     bool                `json:"bundle,omitempty"`
	Providers  []string            `json:"providers,omitempty"`
	HideIcon   bool                `json:"hide_icon,omitempty"`
	Icons      map[string]IconFile `json:"icons,omitempty"`
}

// Client 是插件仓库客户端。
type Client struct {
	URL           string
	Channel       string
	PublicKey     ed25519.PublicKey
	Timeout       time.Duration
	MaxDownloadMB int
	HTTP          *http.Client
}

// ParsePublicKey 解析 base64 编码的 Ed25519 公钥，同时接受带填充与无填充格式。
func ParsePublicKey(value string) (ed25519.PublicKey, error) {
	value = strings.TrimSpace(value)
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("插件仓库公钥格式不正确")
	}
	return ed25519.PublicKey(raw), nil
}

// PublicKeyFingerprint 返回公钥的 SHA256 指纹（SHA256:base64）。
func PublicKeyFingerprint(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// NewClient 创建仓库客户端。publicKey 为空表示不校验索引签名。
func NewClient(rawURL, channel, publicKey string, timeout time.Duration, maxDownloadMB int) (*Client, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("插件仓库地址为空")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if maxDownloadMB <= 0 {
		maxDownloadMB = 256
	}
	client := &Client{
		URL:           rawURL,
		Channel:       channel,
		Timeout:       timeout,
		MaxDownloadMB: maxDownloadMB,
		HTTP:          outbound.NewClient(timeout, false),
	}
	if strings.TrimSpace(publicKey) != "" {
		key, err := ParsePublicKey(publicKey)
		if err != nil {
			return nil, err
		}
		client.PublicKey = key
	}
	return client, nil
}

// Fetch 拉取索引。etag 非空时使用条件请求；notModified 为 true 表示 304。
// signature 是索引的 Ed25519 分离签名（未配置公钥时为空），调用方应把它与
// 原始索引一起缓存，以便 304 时重新验签。
func (c *Client) Fetch(ctx context.Context, etag string) (Index, []byte, string, []byte, bool, error) {
	raw, newETag, notModified, err := c.fetchBytes(ctx, c.URL, etag, maxIndexBytes)
	if err != nil {
		return Index{}, nil, "", nil, false, err
	}
	if notModified {
		return Index{}, nil, etag, nil, true, nil
	}
	var signature []byte
	if c.PublicKey != nil {
		sigRaw, _, _, err := c.fetchBytes(ctx, c.URL+".sig", "", 4096)
		if err != nil {
			return Index{}, nil, "", nil, false, i18n.E("plugin.repo.signature_read_failed", "error", err.Error())
		}
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
		if err != nil {
			return Index{}, nil, "", nil, false, i18n.E("plugin.repo.signature_parse_failed", "error", err.Error())
		}
		if !ed25519.Verify(c.PublicKey, raw, sig) {
			return Index{}, nil, "", nil, false, i18n.E("plugin.repo.signature_invalid")
		}
		signature = sig
	}
	var index Index
	if err := json.Unmarshal(raw, &index); err != nil {
		return Index{}, nil, "", nil, false, i18n.E("plugin.repo.index_parse_failed", "error", err.Error())
	}
	if index.Schema != SchemaVersion {
		return Index{}, nil, "", nil, false, i18n.E("plugin.repo.schema_unsupported", "version", index.Schema)
	}
	if index.Protocol != "" && index.Protocol != ProtocolVersion {
		return Index{}, nil, "", nil, false, i18n.E("plugin.repo.protocol_unsupported", "protocol", index.Protocol)
	}
	index.SanitizeIcons()
	if err := index.Validate(); err != nil {
		return Index{}, nil, "", nil, false, i18n.E("plugin.repo.index_invalid", "error", err.Error())
	}
	return index, raw, newETag, signature, false, nil
}

// VerifyIndexSignature 使用 base64 编码的 Ed25519 公钥校验索引原始字节。
// 304 场景必须重新调用它，不能仅因为“配置了公钥”就认为缓存已验签。
func VerifyIndexSignature(publicKey string, raw, signature []byte) bool {
	key, err := ParsePublicKey(publicKey)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(key, raw, signature)
}

// Validate 校验索引中的插件与资产字段，确保安装前就具备可校验的 sha256。
func (i Index) Validate() error {
	seen := make(map[string]bool, len(i.Plugins))
	for _, plugin := range i.Plugins {
		if plugin.Kind != "dns" && plugin.Kind != "deploy" && plugin.Kind != "notify" {
			return fmt.Errorf("未知插件类型 %q", plugin.Kind)
		}
		if !idPattern.MatchString(plugin.ID) {
			return fmt.Errorf("插件 ID 非法: %q", plugin.ID)
		}
		key := plugin.Kind + "/" + plugin.ID
		if seen[key] {
			return fmt.Errorf("插件重复: %s", key)
		}
		seen[key] = true
		if strings.TrimSpace(plugin.Version) == "" {
			return fmt.Errorf("插件 %s 缺少版本", key)
		}
		if plugin.Bundle {
			if len(plugin.Providers) == 0 {
				return fmt.Errorf("合并插件 %s 缺少 providers", key)
			}
			providerSeen := make(map[string]bool, len(plugin.Providers))
			for _, id := range plugin.Providers {
				id = strings.TrimSpace(id)
				if !idPattern.MatchString(id) {
					return fmt.Errorf("合并插件 %s 的 provider ID 非法: %q", key, id)
				}
				if providerSeen[id] {
					return fmt.Errorf("合并插件 %s 的 provider 重复: %s", key, id)
				}
				providerSeen[id] = true
			}
		}
		if len(plugin.Assets) == 0 {
			return i18n.E("plugin.repo.no_asset", "plugin", key)
		}
		for platform, asset := range plugin.Assets {
			if strings.TrimSpace(asset.URL) == "" {
				return fmt.Errorf("插件 %s 的 %s 资产缺少 URL", key, platform)
			}
			if _, err := normalizeSHA256(asset.SHA256); err != nil {
				return i18n.E("plugin.repo.no_valid_sha256", "plugin", key, "platform", platform)
			}
		}
	}
	return nil
}

func (c *Client) fetchBytes(ctx context.Context, rawURL, etag string, limit int64) ([]byte, string, bool, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", false, err
	}
	if parsed.Scheme == "file" {
		path := filepath.FromSlash(parsed.Path)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, "", false, err
		}
		if int64(len(raw)) > limit {
			return nil, "", false, errors.New("文件超过大小限制")
		}
		return raw, "", false, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", false, err
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, "", false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return nil, etag, true, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("仓库返回 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", false, err
	}
	if int64(len(raw)) > limit {
		return nil, "", false, i18n.E("plugin.repo.index_too_large")
	}
	return raw, response.Header.Get("ETag"), false, nil
}

// Download 下载并强制校验 asset 的 sha256，写入 dest。
// 缺少或格式非法的 sha256 会在发起网络请求前直接拒绝。
func (c *Client) Download(ctx context.Context, asset Asset, dest string) (string, error) {
	if asset.URL == "" {
		return "", errors.New("资产地址为空")
	}
	expected, err := normalizeSHA256(asset.SHA256)
	if err != nil {
		return "", err
	}
	limit := int64(c.MaxDownloadMB) << 20
	parsed, err := url.Parse(asset.URL)
	if err != nil {
		return "", err
	}
	if parsed.User != nil {
		return "", errors.New("插件资产地址不能包含凭据")
	}
	indexURL, err := url.Parse(c.URL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		// HTTPS 索引不应把资产降级到明文 HTTP；sha256 只能保证内容
		// 一致，不能防止链路观察或替换元数据。
		if indexURL.Scheme == "https" {
			return "", i18n.E("plugin.repo.asset_http_forbidden")
		}
	case "file":
		// 只允许 file:// 索引使用本地资产，避免 HTTPS 索引被篡改后
		// 让宿主读取任意本地文件。
		if indexURL.Scheme != "file" {
			return "", i18n.E("plugin.repo.asset_file_forbidden")
		}
	default:
		return "", i18n.E("plugin.repo.asset_scheme_unsupported")
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	success := false
	defer func() {
		_ = out.Close()
		if !success {
			_ = os.Remove(dest)
		}
	}()
	hash := sha256.New()
	var reader io.Reader
	if parsed.Scheme == "file" {
		f, err := os.Open(filepath.FromSlash(parsed.Path))
		if err != nil {
			return "", err
		}
		defer f.Close()
		reader = f
	} else {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
		if err != nil {
			return "", err
		}
		response, err := c.HTTP.Do(request)
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("下载插件返回 HTTP %d", response.StatusCode)
		}
		reader = response.Body
	}
	written, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(reader, limit+1))
	if err != nil {
		return "", err
	}
	if written > limit {
		return "", fmt.Errorf("插件包超过 %d MB 限制", c.MaxDownloadMB)
	}
	if asset.Size > 0 && written != asset.Size {
		return "", fmt.Errorf("插件包大小不符：期望 %d，实际 %d", asset.Size, written)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(expected, digest) {
		return "", fmt.Errorf("插件包 sha256 校验失败：期望 %s，实际 %s", expected, digest)
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	success = true
	return digest, nil
}

// normalizeSHA256 校验并归一化期望的 sha256；缺失或格式错误直接拒绝。
func normalizeSHA256(value string) (string, error) {
	expected := strings.ToLower(strings.TrimSpace(value))
	if expected == "" {
		return "", i18n.E("plugin.repo.missing_sha256")
	}
	if len(expected) != sha256.Size*2 {
		return "", errors.New("插件包 sha256 格式不正确")
	}
	raw, err := hex.DecodeString(expected)
	if err != nil || len(raw) != sha256.Size {
		return "", errors.New("插件包 sha256 格式不正确")
	}
	return expected, nil
}

// SelectAsset 返回当前平台的资产。
func SelectAsset(plugin Plugin, goos, goarch string) (Asset, bool) {
	asset, ok := plugin.Assets[goos+"/"+goarch]
	return asset, ok
}

// Compatible 判断插件是否兼容当前宿主版本与协议。
func Compatible(plugin Plugin, hostVersion string) bool {
	if plugin.Protocol != "" && plugin.Protocol != ProtocolVersion {
		return false
	}
	if strings.TrimSpace(plugin.MinHost) == "" {
		return true
	}
	// 宿主版本可能带 git describe 后缀（如 1.2.0-3-gabc），
	// 兼容性只比较数字核心，避免把构建后缀误判为预发布版本。
	return compareCore(hostVersion, plugin.MinHost) >= 0
}

func compareCore(a, b string) int {
	coreA, _ := splitVersion(a)
	coreB, _ := splitVersion(b)
	for i := 0; i < 3; i++ {
		if coreA[i] > coreB[i] {
			return 1
		}
		if coreA[i] < coreB[i] {
			return -1
		}
	}
	return 0
}

// CompareVersion 比较两个版本号。只比较数字段，预发布后缀视为小于同版本正式版。
func CompareVersion(a, b string) int {
	coreA, preA := splitVersion(a)
	coreB, preB := splitVersion(b)
	for i := 0; i < 3; i++ {
		if coreA[i] > coreB[i] {
			return 1
		}
		if coreA[i] < coreB[i] {
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
	if preA > preB {
		return 1
	}
	if preA < preB {
		return -1
	}
	return 0
}

func splitVersion(value string) ([3]int, string) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	core := value
	pre := ""
	if idx := strings.IndexAny(core, "-+"); idx >= 0 {
		pre = core[idx+1:]
		core = core[:idx]
	}
	numbers := make([]int, 0, 3)
	for _, part := range strings.SplitN(core, ".", 3) {
		number, _ := strconv.Atoi(strings.TrimSpace(part))
		numbers = append(numbers, number)
	}
	var out [3]int
	copy(out[:], numbers)
	return out, pre
}
