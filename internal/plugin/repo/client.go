// Package pluginrepo 实现插件仓库索引、下载、校验与解包。
package pluginrepo

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	SourceHash       string           `json:"source_hash,omitempty"`
	Protocol         string           `json:"protocol"`
	MinHost          string           `json:"min_host,omitempty"`
	Bundle           bool             `json:"bundle,omitempty"`
	Providers        []string         `json:"providers,omitempty"`
	VariantOf        string           `json:"variant_of,omitempty"`
	ConflictsWith    []string         `json:"conflicts_with,omitempty"`
	SDK              string           `json:"sdk,omitempty"`
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
	Revision    uint64   `json:"revision,omitempty"`
	GeneratedAt string   `json:"generated_at"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
	RepoID      string   `json:"repo_id,omitempty"`
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
	Proxy         string
	PublicKeys    []ed25519.PublicKey
	Timeout       time.Duration
	MaxDownloadMB int
	HTTP          *http.Client
}

// ParsePublicKey 解析单个 base64 Ed25519 公钥。
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

// ParsePublicKeys 解析一个或多个 base64 Ed25519 公钥，支持用逗号、空白或
// 换行分隔，便于在不改配置结构的情况下做双密钥过渡。
func ParsePublicKeys(value string) ([]ed25519.PublicKey, error) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	keys := make([]ed25519.PublicKey, 0, len(fields))
	for _, field := range fields {
		key, err := ParsePublicKey(field)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// PublicKeyFingerprint 返回公钥的 SHA256 指纹（SHA256:base64）。
func PublicKeyFingerprint(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// NewClient 创建仓库客户端。publicKey 为空表示不校验索引签名。
func NewClient(rawURL, channel, publicKey string, timeout time.Duration, maxDownloadMB int) (*Client, error) {
	return NewClientWithProxy(rawURL, channel, publicKey, timeout, maxDownloadMB, "")
}

// NewClientWithProxy 创建带显式下载代理的仓库客户端。
// proxy 为空时沿用 HTTP(S)_PROXY/ALL_PROXY 环境变量；非空时支持
// http、https、socks5 与 socks5h，且不再应用 NO_PROXY。
func NewClientWithProxy(rawURL, channel, publicKey string, timeout time.Duration, maxDownloadMB int, proxy string) (*Client, error) {
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
	proxy = strings.TrimSpace(proxy)
	httpClient, err := outbound.NewClientWithProxy(timeout, false, proxy)
	if err != nil {
		return nil, err
	}
	client := &Client{
		URL:           rawURL,
		Channel:       channel,
		Proxy:         proxy,
		Timeout:       timeout,
		MaxDownloadMB: maxDownloadMB,
		HTTP:          httpClient,
	}
	if strings.TrimSpace(publicKey) != "" {
		keys, err := ParsePublicKeys(publicKey)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			return nil, errors.New("插件仓库公钥格式不正确")
		}
		client.PublicKeys = keys
	}
	return client, nil
}

// Fetch 拉取索引并只返回可用条目。etag 非空时使用条件请求；
// notModified 为 true 表示 304。需要保留被隔离条目与问题列表时使用 FetchValidated。
func (c *Client) Fetch(ctx context.Context, etag string) (Index, []byte, string, []byte, bool, error) {
	result, raw, newETag, signature, notModified, err := c.FetchValidated(ctx, etag, ParseOptions{})
	return result.Index, raw, newETag, signature, notModified, err
}

// FetchValidated 拉取索引，统一执行签名校验、schema/protocol/channel 校验与逐条准入。
// signature 是索引的 Ed25519 分离签名（未配置公钥时为空），调用方应把它与
// 原始索引一起缓存，以便 304 时重新验签。
func (c *Client) FetchValidated(ctx context.Context, etag string, opts ParseOptions) (ValidatedIndex, []byte, string, []byte, bool, error) {
	if opts.Channel == "" {
		opts.Channel = c.Channel
	}
	if opts.IndexURL == "" {
		opts.IndexURL = c.URL
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = maxIndexBytes
	}
	raw, newETag, notModified, err := c.fetchBytes(ctx, c.URL, etag, int64(opts.MaxBytes))
	if err != nil {
		return ValidatedIndex{}, nil, "", nil, false, err
	}
	if notModified {
		return ValidatedIndex{}, nil, etag, nil, true, nil
	}
	var signature []byte
	var keyID string
	if len(c.PublicKeys) > 0 {
		signature, keyID, err = c.fetchSignature(ctx, raw)
		if err != nil {
			return ValidatedIndex{}, nil, "", nil, false, err
		}
	}
	result, err := ParseIndex(raw, opts)
	if err != nil {
		return result, nil, "", nil, false, err
	}
	result.KeyID = keyID
	return result, raw, newETag, signature, false, nil
}

// fetchSignature 优先读取 index.json.sig.json 封套，不存在时回退到
// 旧格式 index.json.sig。封套存在但校验失败时不会降级到旧签名。
func (c *Client) fetchSignature(ctx context.Context, raw []byte) ([]byte, string, error) {
	if envelopeURL, err := withSuffix(c.URL, ".sig.json"); err == nil {
		body, found, fetchErr := c.fetchOptionalBytes(ctx, envelopeURL, maxSignatureBytes)
		if fetchErr != nil {
			// 5xx/401/网络错误不能被当成“封套不存在”而静默回退，
			// 否则会掩盖仓库权限或代理问题。
			return nil, "", i18n.E("plugin.repo.signature_read_failed", "error", fetchErr.Error())
		}
		if found {
			keyID, ok := verifySignatureBlob(c.PublicKeys, raw, body)
			if !ok {
				return nil, "", i18n.E("plugin.repo.signature_invalid")
			}
			return body, keyID, nil
		}
	}
	sigURL, err := signatureURL(c.URL)
	if err != nil {
		return nil, "", i18n.E("plugin.repo.signature_read_failed", "error", err.Error())
	}
	sigRaw, _, _, err := c.fetchBytes(ctx, sigURL, "", 4096)
	if err != nil {
		return nil, "", i18n.E("plugin.repo.signature_read_failed", "error", err.Error())
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
	if err != nil {
		return nil, "", i18n.E("plugin.repo.signature_parse_failed", "error", err.Error())
	}
	keyID, ok := verifySignatureBlob(c.PublicKeys, raw, sig)
	if !ok {
		return nil, "", i18n.E("plugin.repo.signature_invalid")
	}
	return sig, keyID, nil
}

// fetchOptionalBytes 拉取可选文件；不存在（包括 S3 对缺失 key 返回的 403）时 found=false。
func (c *Client) fetchOptionalBytes(ctx context.Context, rawURL string, limit int64) ([]byte, bool, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, false, err
	}
	if parsed.Scheme == "file" {
		raw, err := os.ReadFile(filepath.FromSlash(parsed.Path))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, false, nil
			}
			return nil, false, err
		}
		if int64(len(raw)) > limit {
			return nil, false, errors.New("签名文件超过大小限制")
		}
		return raw, true, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, false, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("仓库返回 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(raw)) > limit {
		return nil, false, errors.New("签名文件超过大小限制")
	}
	return raw, true, nil
}

// signatureURL 在索引路径后追加 .sig，而不是拼接整个 URL。
// 私有仓库常把 token 放在 query 中，直接 c.URL+".sig" 会把后缀写进 query。
func signatureURL(raw string) (string, error) {
	return withSuffix(raw, ".sig")
}

func withSuffix(raw, suffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	parsed.Path += suffix
	parsed.RawPath = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

// PublicKeysFingerprint 返回多个公钥的稳定指纹串；单密钥时保持旧格式。
func PublicKeysFingerprint(keys []ed25519.PublicKey) string {
	if len(keys) == 0 {
		return ""
	}
	if len(keys) == 1 {
		return PublicKeyFingerprint(keys[0])
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, PublicKeyFingerprint(key))
	}
	return strings.Join(parts, ",")
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
	// 与 ParseIndex 共用同一套协议/凭据规则，避免列表阶段接受、下载阶段才失败。
	if detail := validateAssetURL(asset.URL, c.URL); detail != nil {
		return "", detailError(detail)
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

// gitDescribeSuffix 匹配 1.2.0-3-gabc 这类构建后缀。
var gitDescribeSuffix = regexp.MustCompile(`^\d+-g[0-9a-fA-F]+(?:-dirty)?$`)

// Compatible 判断插件是否兼容当前宿主版本与协议。
func Compatible(plugin Plugin, hostVersion string) bool {
	if plugin.Protocol != "" && plugin.Protocol != ProtocolVersion {
		return false
	}
	if strings.TrimSpace(plugin.MinHost) == "" {
		return true
	}
	// 宿主版本可能带 git describe 后缀（如 1.2.0-3-gabc），
	// 这类构建后缀不代表预发布，只比较数字核心；真正的预发布
	// （如 1.0.0-beta）必须低于同版本正式版。
	_, hostPre := splitVersion(hostVersion)
	if hostPre != "" && !gitDescribeSuffix.MatchString(hostPre) {
		return CompareVersion(hostVersion, plugin.MinHost) >= 0
	}
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
