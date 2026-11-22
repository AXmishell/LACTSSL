package pluginrepo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"lactssl/internal/i18n"
)

// Severity 表示一条索引校验问题的处理级别。
type Severity string

const (
	// SeverityWarn 表示条目仍可使用，只提示元数据问题。
	SeverityWarn Severity = "warn"
	// SeverityEntry 表示只隔离该条目，其余条目仍可使用。
	SeverityEntry Severity = "entry"
	// SeverityFatal 表示整份索引不可用。
	SeverityFatal Severity = "fatal"
)

var severityOrder = map[Severity]int{
	SeverityFatal: 0,
	SeverityEntry: 1,
	SeverityWarn:  2,
}

// sortIssues 让同一份索引每次产生稳定顺序，便于 UI 展示与测试断言。
func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		left, right := issues[i], issues[j]
		if severityOrder[left.Severity] != severityOrder[right.Severity] {
			return severityOrder[left.Severity] < severityOrder[right.Severity]
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Plugin != right.Plugin {
			return left.Plugin < right.Plugin
		}
		return left.Platform < right.Platform
	})
}

const (
	// MaxIndexPlugins 是单份索引允许的插件条目上限。
	MaxIndexPlugins = 4096
	// MaxAssetsPerPlugin 是单个插件允许的平台资产上限。
	MaxAssetsPerPlugin = 64
	// MaxIndexIcons 是单份索引允许的内联图标上限。
	MaxIndexIcons = 2048
	// MaxIndexIssues 是返回给调用方的最大问题条数，避免响应被恶意索引放大。
	MaxIndexIssues = 100
)

var versionPattern = regexp.MustCompile(`^[vV]?\d+(?:\.\d+){0,2}(?:[-+][0-9A-Za-z][0-9A-Za-z.-]*)?$`)

// 已知的 GOOS/GOARCH 组合。索引里出现其他值时宿主永远选不中，
// 尽早隔离可以避免发布侧把 `amd65` 这类拼写错误带到用户端。
var knownGOOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true,
	"hurd": true, "illumos": true, "ios": true, "js": true, "linux": true,
	"netbsd": true, "openbsd": true, "plan9": true, "solaris": true, "wasip1": true,
	"windows": true, "zos": true,
}

var knownGOARCH = map[string]bool{
	"386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true,
	"mips": true, "mips64": true, "mips64le": true, "mipsle": true,
	"ppc64": true, "ppc64le": true, "riscv64": true, "s390x": true, "wasm": true,
}

func validPlatformKey(key string) bool {
	goos, goarch, ok := strings.Cut(key, "/")
	return ok && knownGOOS[goos] && knownGOARCH[goarch]
}

// IssueDetail 是 Issue 的结构化子原因。宿主只保存 code 与参数，
// 渲染时再按请求语言翻译，避免把中文文案写进 Params 或日志。
type IssueDetail struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// issueDetail 构造一个结构化子原因。
func issueDetail(code string, pairs ...any) *IssueDetail {
	detail := &IssueDetail{Code: code}
	if len(pairs) > 0 {
		detail.Params = make(map[string]any, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			name, ok := pairs[i].(string)
			if !ok {
				continue
			}
			detail.Params[name] = pairs[i+1]
		}
	}
	return detail
}

// detailError 把结构化子原因转换为可翻译错误。
func detailError(detail *IssueDetail) error {
	if detail == nil {
		return nil
	}
	return &i18n.Error{Key: detail.Code, Params: detail.Params}
}

// Issue 是一条结构化索引校验问题。
// Code 是稳定的 i18n key，Path 用于定位到具体字段，Detail 是可翻译子原因。
type Issue struct {
	Code     string         `json:"code"`
	Severity Severity       `json:"severity"`
	Path     string         `json:"path,omitempty"`
	Kind     string         `json:"kind,omitempty"`
	Plugin   string         `json:"plugin,omitempty"`
	Platform string         `json:"platform,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
	Detail   *IssueDetail   `json:"detail,omitempty"`
}

func (i Issue) toError() *i18n.Error {
	localized := &i18n.Error{Key: i.Code, Params: i.Params}
	if i.Detail != nil {
		localized.Detail = &i18n.Error{Key: i.Detail.Code, Params: i.Detail.Params}
	}
	return localized
}

// Error 把问题转换为可翻译错误，便于 API 层直接返回。
func (i Issue) Error() error {
	return i.toError()
}

// Text 按语言渲染问题文案。
func (i Issue) Text(locale i18n.Locale) string {
	return i.toError().Text(locale)
}

// DetailText 按语言渲染子原因；没有子原因时返回空串。
func (i Issue) DetailText(locale i18n.Locale) string {
	return i.toError().DetailText(locale)
}

// RejectedEntry 是一条被隔离的插件条目及首要原因。
type RejectedEntry struct {
	Plugin Plugin `json:"plugin"`
	Reason Issue  `json:"reason"`
}

// IndexAudit 记录一次安装所依据的签名索引身份。
type IndexAudit struct {
	Revision    uint64 `json:"index_revision,omitempty"`
	IndexDigest string `json:"index_digest,omitempty"`
	EntryDigest string `json:"entry_digest,omitempty"`
}

// Audit 生成某个插件条目的安装审计信息。
func (v ValidatedIndex) Audit(plugin Plugin) IndexAudit {
	return IndexAudit{
		Revision:    v.Revision,
		IndexDigest: v.Digest,
		EntryDigest: PluginDigest(plugin),
	}
}

// PluginDigest 返回插件条目的规范化 JSON 摘要。
// encoding/json 对结构体字段和 map 键排序稳定，因此同一份条目内容会得到相同摘要。
func PluginDigest(plugin Plugin) string {
	raw, err := json.Marshal(plugin)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ValidatedIndex 是解析与校验后的索引。
// Index.Plugins 只包含可用条目；被隔离条目保留在 Rejected 中供 UI 展示。
type ValidatedIndex struct {
	Index       Index
	Revision    uint64
	GeneratedAt time.Time
	ExpiresAt   time.Time
	Digest      string
	KeyID       string
	Issues      []Issue
	Rejected    []RejectedEntry
}

// Expired 判断索引是否已过期。没有 expires_at 的旧索引始终视为未过期，
// 是否允许安装由调用方结合回滚/新鲜度策略决定。
func (v ValidatedIndex) Expired(now time.Time) bool {
	return !v.ExpiresAt.IsZero() && now.After(v.ExpiresAt)
}

// ParseOptions 控制索引解析与校验。
type ParseOptions struct {
	Channel          string
	IndexURL         string
	LastRevision     uint64
	LastGeneratedAt  time.Time
	Now              time.Time
	MaxBytes         int
	MaxPlugins       int
	MaxAssets        int
	MaxIcons         int
	MaxDownloadBytes int64
	StrictExpiry     bool
}

func (o ParseOptions) normalized() ParseOptions {
	if o.Now.IsZero() {
		o.Now = time.Now().UTC()
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = maxIndexBytes
	}
	if o.MaxPlugins <= 0 {
		o.MaxPlugins = MaxIndexPlugins
	}
	if o.MaxAssets <= 0 {
		o.MaxAssets = MaxAssetsPerPlugin
	}
	if o.MaxIcons <= 0 {
		o.MaxIcons = MaxIndexIcons
	}
	return o
}

// ParseIndex 解析并校验索引。只有 Fatal 问题返回 error；
// 条目级问题会进入 Rejected，调用方仍可继续使用其余条目。
func ParseIndex(raw []byte, opts ParseOptions) (ValidatedIndex, error) {
	opts = opts.normalized()
	result := ValidatedIndex{Digest: indexDigest(raw)}
	if len(raw) == 0 {
		return failIndex(result, Issue{
			Code: "plugin.repo.index_parse_failed", Severity: SeverityFatal,
			Detail: issueDetail("plugin.repo.detail.index_empty"),
		})
	}
	if int64(len(raw)) > int64(opts.MaxBytes) {
		return failIndex(result, Issue{Code: "plugin.repo.index_too_large", Severity: SeverityFatal})
	}
	var index Index
	if err := json.Unmarshal(raw, &index); err != nil {
		return failIndex(result, Issue{
			Code: "plugin.repo.index_parse_failed", Severity: SeverityFatal,
			Detail: issueDetail("plugin.repo.detail.index_json_invalid", "error", err.Error()),
		})
	}
	result.Index = index
	if index.Schema != SchemaVersion {
		return failIndex(result, Issue{
			Code: "plugin.repo.schema_unsupported", Severity: SeverityFatal,
			Params: map[string]any{"version": index.Schema},
		})
	}
	if index.Protocol != "" && index.Protocol != ProtocolVersion {
		return failIndex(result, Issue{
			Code: "plugin.repo.protocol_unsupported", Severity: SeverityFatal,
			Params: map[string]any{"protocol": index.Protocol},
		})
	}
	if opts.Channel != "" && index.Channel != "" && !strings.EqualFold(strings.TrimSpace(index.Channel), strings.TrimSpace(opts.Channel)) {
		return failIndex(result, Issue{
			Code: "plugin.repo.channel_mismatch", Severity: SeverityFatal,
			Params: map[string]any{"channel": index.Channel, "expected": opts.Channel},
		})
	}
	if index.Revision > 0 {
		result.Revision = index.Revision
	}
	if opts.LastRevision > 0 && index.Revision > 0 && index.Revision < opts.LastRevision {
		return failIndex(result, Issue{
			Code: "plugin.repo.rollback_detected", Severity: SeverityFatal,
			Params: map[string]any{"revision": index.Revision, "accepted": opts.LastRevision},
		})
	}
	if opts.LastRevision > 0 && index.Revision == 0 {
		// 本地已经接受过带 revision 的索引后，突然缺失 revision 的旧索引
		// 可能是重放。此时不能只告警，否则防回滚可被旧签名索引绕过。
		return failIndex(result, Issue{
			Code: "plugin.repo.revision_missing", Severity: SeverityFatal,
			Params: map[string]any{"accepted": opts.LastRevision},
		})
	}
	if index.GeneratedAt != "" {
		generated, err := time.Parse(time.RFC3339, index.GeneratedAt)
		if err != nil {
			result.Issues = append(result.Issues, Issue{
				Code: "plugin.repo.invalid_generated_at", Severity: SeverityWarn,
				Params: map[string]any{"value": index.GeneratedAt},
			})
		} else {
			result.GeneratedAt = generated
			if !opts.LastGeneratedAt.IsZero() && generated.Before(opts.LastGeneratedAt) {
				result.Issues = append(result.Issues, Issue{
					Code: "plugin.repo.rollback_suspected", Severity: SeverityWarn,
					Params: map[string]any{"generated_at": index.GeneratedAt, "accepted": opts.LastGeneratedAt.Format(time.RFC3339)},
				})
			}
		}
	}
	if index.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339, index.ExpiresAt)
		if err != nil {
			result.Issues = append(result.Issues, Issue{
				Code: "plugin.repo.invalid_expires_at", Severity: SeverityWarn,
				Params: map[string]any{"value": index.ExpiresAt},
			})
		} else {
			result.ExpiresAt = expires
			if !expires.IsZero() && opts.Now.After(expires) {
				severity := SeverityWarn
				if opts.StrictExpiry {
					severity = SeverityFatal
				}
				issue := Issue{
					Code: "plugin.repo.index_expired", Severity: severity,
					Params: map[string]any{"time": expires.Format(time.RFC3339)},
				}
				if severity == SeverityFatal {
					return failIndex(result, issue)
				}
				result.Issues = append(result.Issues, issue)
			}
		}
	}
	if len(index.Plugins) > opts.MaxPlugins {
		return failIndex(result, Issue{
			Code: "plugin.repo.entry_limit", Severity: SeverityFatal,
			Params: map[string]any{"count": len(index.Plugins), "limit": opts.MaxPlugins},
		})
	}
	if len(index.Icons) > opts.MaxIcons {
		return failIndex(result, Issue{
			Code: "plugin.repo.icon_limit", Severity: SeverityFatal,
			Params: map[string]any{"count": len(index.Icons), "limit": opts.MaxIcons},
		})
	}
	result.Issues = append(result.Issues, index.sanitizeIcons()...)
	result.Issues = append(result.Issues, detectUnknownFields(raw)...)

	accepted, rejected, issues, fatal := validateEntries(index, opts)
	result.Index.Plugins = accepted
	result.Rejected = rejected
	result.Issues = append(result.Issues, issues...)
	sortIssues(result.Issues)
	if len(result.Issues) > MaxIndexIssues {
		result.Issues = result.Issues[:MaxIndexIssues]
	}
	if fatal != nil {
		return result, fatal.Error()
	}
	return result, nil
}

// Validate 保留旧接口：只校验条目，并把首个条目级/致命问题作为 error 返回。
// 文档级 schema/protocol/channel 校验由 ParseIndex 负责。
func (i Index) Validate() error {
	_, _, issues, fatal := validateEntries(i, ParseOptions{}.normalized())
	if fatal != nil {
		return fatal.Error()
	}
	for _, issue := range issues {
		if issue.Severity == SeverityEntry || issue.Severity == SeverityFatal {
			return issue.Error()
		}
	}
	return nil
}

// ValidateEntry 只校验单个插件条目，供安装前复核字段合法性。
func ValidateEntry(plugin Plugin, opts ParseOptions) []Issue {
	opts = opts.normalized()
	issues := validatePluginFields(plugin, "plugin", opts)
	sortIssues(issues)
	return issues
}

// HasBlockingIssue 判断问题列表中是否存在会阻止安装的条目级问题。
func HasBlockingIssue(issues []Issue) bool {
	for _, issue := range issues {
		if issue.Severity == SeverityEntry || issue.Severity == SeverityFatal {
			return true
		}
	}
	return false
}

func failIndex(result ValidatedIndex, issue Issue) (ValidatedIndex, error) {
	result.Issues = append([]Issue{issue}, result.Issues...)
	sortIssues(result.Issues)
	if len(result.Issues) > MaxIndexIssues {
		result.Issues = result.Issues[:MaxIndexIssues]
	}
	return result, issue.Error()
}

func indexDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validateEntries(index Index, opts ParseOptions) ([]Plugin, []RejectedEntry, []Issue, *Issue) {
	byKey := make(map[string]Plugin, len(index.Plugins))
	seen := make(map[string]bool, len(index.Plugins))
	issues := make([]Issue, 0)
	rejected := make([]RejectedEntry, 0)
	accepted := make([]Plugin, 0, len(index.Plugins))

	for idx, plugin := range index.Plugins {
		path := fmt.Sprintf("plugins[%d]", idx)
		if plugin.Kind != "dns" && plugin.Kind != "deploy" && plugin.Kind != "notify" {
			issue := Issue{
				Code: "plugin.repo.invalid_kind", Severity: SeverityEntry,
				Path: path + ".kind", Kind: plugin.Kind, Plugin: plugin.ID,
				Params: map[string]any{"kind": plugin.Kind},
			}
			issues = append(issues, issue)
			rejected = append(rejected, RejectedEntry{Plugin: plugin, Reason: issue})
			continue
		}
		if !idPattern.MatchString(plugin.ID) {
			issue := Issue{
				Code: "plugin.repo.invalid_id", Severity: SeverityEntry,
				Path: path + ".id", Kind: plugin.Kind, Plugin: plugin.ID,
				Params: map[string]any{"id": plugin.ID},
			}
			issues = append(issues, issue)
			rejected = append(rejected, RejectedEntry{Plugin: plugin, Reason: issue})
			continue
		}
		key := plugin.Kind + "/" + plugin.ID
		if seen[key] {
			issue := Issue{
				Code: "plugin.repo.duplicate_plugin", Severity: SeverityFatal,
				Path: path, Kind: plugin.Kind, Plugin: plugin.ID,
				Params: map[string]any{"plugin": key},
			}
			return nil, nil, issues, &issue
		}
		seen[key] = true
		byKey[key] = plugin

		entryIssues := validatePluginFields(plugin, path, opts)
		var blocked *Issue
		for i := range entryIssues {
			issues = append(issues, entryIssues[i])
			if blocked == nil && entryIssues[i].Severity == SeverityEntry {
				blocked = &entryIssues[i]
			}
		}
		if blocked != nil {
			rejected = append(rejected, RejectedEntry{Plugin: plugin, Reason: *blocked})
			continue
		}
		accepted = append(accepted, plugin)
	}

	// 跨条目关系只能在所有 kind/id 确定后校验，避免把“引用稍后定义”的条目误判为坏数据。
	for _, plugin := range accepted {
		if plugin.VariantOf != "" {
			if _, ok := byKey[plugin.Kind+"/"+plugin.VariantOf]; !ok {
				issues = append(issues, Issue{
					Code: "plugin.repo.variant_missing", Severity: SeverityWarn,
					Kind: plugin.Kind, Plugin: plugin.ID,
					Params: map[string]any{"target": plugin.VariantOf},
				})
			}
		}
		for _, conflict := range plugin.ConflictsWith {
			target, ok := byKey[plugin.Kind+"/"+conflict]
			if !ok {
				issues = append(issues, Issue{
					Code: "plugin.repo.conflict_missing", Severity: SeverityWarn,
					Kind: plugin.Kind, Plugin: plugin.ID,
					Params: map[string]any{"target": conflict},
				})
				continue
			}
			if !containsString(target.ConflictsWith, plugin.ID) {
				issues = append(issues, Issue{
					Code: "plugin.repo.conflict_asymmetric", Severity: SeverityWarn,
					Kind: plugin.Kind, Plugin: plugin.ID,
					Params: map[string]any{"target": conflict},
				})
			}
		}
	}
	return accepted, rejected, issues, nil
}

func validatePluginFields(plugin Plugin, path string, opts ParseOptions) []Issue {
	issues := make([]Issue, 0)
	addDetail := func(code string, severity Severity, field string, platform string, params map[string]any, detail *IssueDetail) {
		issues = append(issues, Issue{
			Code: code, Severity: severity,
			Path: path + field, Kind: plugin.Kind, Plugin: plugin.ID,
			Platform: platform, Params: params, Detail: detail,
		})
	}
	add := func(code string, severity Severity, field string, platform string, params map[string]any) {
		addDetail(code, severity, field, platform, params, nil)
	}

	if !versionPattern.MatchString(strings.TrimSpace(plugin.Version)) {
		add("plugin.repo.invalid_version", SeverityEntry, ".version", "",
			map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "version": plugin.Version})
	}
	if plugin.MinHost != "" && !versionPattern.MatchString(strings.TrimSpace(plugin.MinHost)) {
		add("plugin.repo.invalid_min_host", SeverityEntry, ".min_host", "",
			map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "version": plugin.MinHost})
	}
	if plugin.Protocol != "" && plugin.Protocol != ProtocolVersion {
		add("plugin.repo.entry_protocol_unsupported", SeverityEntry, ".protocol", "",
			map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "protocol": plugin.Protocol})
	}
	if plugin.Bundle {
		providerSeen := make(map[string]bool, len(plugin.Providers))
		switch {
		case len(plugin.Providers) == 0:
			addDetail("plugin.repo.invalid_providers", SeverityEntry, ".providers", "",
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID},
				issueDetail("plugin.repo.detail.providers_missing"))
		default:
			for _, id := range plugin.Providers {
				id = strings.TrimSpace(id)
				if !idPattern.MatchString(id) {
					addDetail("plugin.repo.invalid_providers", SeverityEntry, ".providers", "",
						map[string]any{"plugin": plugin.Kind + "/" + plugin.ID},
						issueDetail("plugin.repo.detail.provider_id_invalid", "id", id))
					break
				}
				if providerSeen[id] {
					addDetail("plugin.repo.invalid_providers", SeverityEntry, ".providers", "",
						map[string]any{"plugin": plugin.Kind + "/" + plugin.ID},
						issueDetail("plugin.repo.detail.provider_duplicate", "id", id))
					break
				}
				providerSeen[id] = true
			}
		}
	}
	if plugin.Single && plugin.Bundle {
		add("plugin.repo.invalid_single", SeverityEntry, ".single", "",
			map[string]any{"plugin": plugin.Kind + "/" + plugin.ID})
	}
	if plugin.VariantOf != "" {
		switch {
		case !idPattern.MatchString(plugin.VariantOf):
			addDetail("plugin.repo.invalid_variant", SeverityEntry, ".variant_of", "",
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID},
				issueDetail("plugin.repo.detail.variant_invalid", "value", plugin.VariantOf))
		case plugin.VariantOf == plugin.ID:
			addDetail("plugin.repo.invalid_variant", SeverityEntry, ".variant_of", "",
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID},
				issueDetail("plugin.repo.detail.variant_self"))
		}
	}
	conflictSeen := make(map[string]bool, len(plugin.ConflictsWith))
	for _, id := range plugin.ConflictsWith {
		switch {
		case !idPattern.MatchString(id):
			addDetail("plugin.repo.invalid_conflict", SeverityEntry, ".conflicts_with", "",
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID},
				issueDetail("plugin.repo.detail.conflict_id_invalid", "id", id))
		case id == plugin.ID:
			addDetail("plugin.repo.invalid_conflict", SeverityEntry, ".conflicts_with", "",
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID},
				issueDetail("plugin.repo.detail.conflict_self"))
		case conflictSeen[id]:
			addDetail("plugin.repo.invalid_conflict", SeverityEntry, ".conflicts_with", "",
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID},
				issueDetail("plugin.repo.detail.conflict_duplicate", "id", id))
		}
		conflictSeen[id] = true
	}
	if plugin.SourceHash != "" && !isSHA256Hex(plugin.SourceHash) {
		add("plugin.repo.invalid_source_hash", SeverityWarn, ".source_hash", "",
			map[string]any{"plugin": plugin.Kind + "/" + plugin.ID})
	}
	if plugin.Deprecated {
		add("plugin.repo.deprecated_plugin", SeverityWarn, "", "",
			map[string]any{"plugin": plugin.Kind + "/" + plugin.ID})
	}
	if plugin.UncompressedSize < 0 {
		add("plugin.repo.invalid_uncompressed_size", SeverityWarn, ".uncompressed_size", "",
			map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "size": plugin.UncompressedSize})
	}
	if len(plugin.Assets) == 0 {
		add("plugin.repo.no_asset", SeverityEntry, ".assets", "",
			map[string]any{"plugin": plugin.Kind + "/" + plugin.ID})
		return issues
	}
	if len(plugin.Assets) > opts.MaxAssets {
		add("plugin.repo.asset_limit", SeverityEntry, ".assets", "",
			map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "count": len(plugin.Assets), "limit": opts.MaxAssets})
		return issues
	}
	for platform, asset := range plugin.Assets {
		if !validPlatformKey(platform) {
			add("plugin.repo.invalid_platform", SeverityEntry, ".assets", platform,
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "platform": platform})
			continue
		}
		if detail := validateAssetURL(asset.URL, opts.IndexURL); detail != nil {
			addDetail("plugin.repo.invalid_asset_url", SeverityEntry, ".assets", platform,
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "platform": platform},
				detail)
		}
		if _, err := normalizeSHA256(asset.SHA256); err != nil {
			add("plugin.repo.no_valid_sha256", SeverityEntry, ".assets", platform,
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "platform": platform})
		}
		if asset.Size < 0 {
			add("plugin.repo.invalid_asset_size", SeverityEntry, ".assets", platform,
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "platform": platform, "size": asset.Size})
		}
		if opts.MaxDownloadBytes > 0 && asset.Size > opts.MaxDownloadBytes {
			add("plugin.repo.asset_too_large", SeverityEntry, ".assets", platform,
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "platform": platform, "size": asset.Size})
		}
		if asset.UncompressedSize < 0 {
			add("plugin.repo.invalid_uncompressed_size", SeverityWarn, ".assets", platform,
				map[string]any{"plugin": plugin.Kind + "/" + plugin.ID, "platform": platform, "size": asset.UncompressedSize})
		}
	}
	return issues
}

func isSHA256Hex(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// validateAssetURL 校验资产地址是否允许在给定索引来源下使用。
// Fetch/ParseIndex 与 Download 共用同一套规则，避免列表阶段接受、下载阶段失败。
// 返回结构化子原因，便于索引问题按语言渲染，也便于下载路径转换为错误。
func validateAssetURL(raw, indexURL string) *IssueDetail {
	if strings.TrimSpace(raw) == "" {
		return issueDetail("plugin.repo.asset_url_empty")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return issueDetail("plugin.repo.detail.asset_url_parse_failed", "error", err.Error())
	}
	if parsed.User != nil {
		return issueDetail("plugin.repo.asset_url_credentials")
	}
	index, _ := url.Parse(strings.TrimSpace(indexURL))
	switch parsed.Scheme {
	case "https":
		if parsed.Host == "" {
			return issueDetail("plugin.repo.asset_url_invalid")
		}
	case "http":
		if parsed.Host == "" {
			return issueDetail("plugin.repo.asset_url_invalid")
		}
		// HTTPS 索引不应把资产降级到明文 HTTP。
		if index != nil && index.Scheme == "https" {
			return issueDetail("plugin.repo.asset_http_forbidden")
		}
	case "file":
		// 只允许 file:// 索引使用本地资产。
		if index == nil || index.Scheme != "file" {
			return issueDetail("plugin.repo.asset_file_forbidden")
		}
	default:
		return issueDetail("plugin.repo.asset_scheme_unsupported")
	}
	return nil
}

// detectUnknownFields 找出宿主不认识的字段，避免发布侧写入的字段被静默丢弃。
func detectUnknownFields(raw []byte) []Issue {
	issues := make([]Issue, 0)
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil
	}
	knownTop := map[string]bool{
		"schema": true, "channel": true, "protocol": true, "generated_at": true,
		"revision": true, "expires_at": true, "repo_id": true, "plugins": true, "icons": true,
	}
	for _, key := range sortedKeys(top) {
		if !knownTop[key] {
			issues = append(issues, Issue{
				Code: "plugin.repo.unknown_field", Severity: SeverityWarn,
				Path: key, Params: map[string]any{"field": key},
			})
		}
	}
	var plugins []json.RawMessage
	if json.Unmarshal(top["plugins"], &plugins) != nil {
		return issues
	}
	knownPlugin := map[string]bool{
		"kind": true, "id": true, "name": true, "description": true, "version": true,
		"source_hash": true, "protocol": true, "min_host": true, "bundle": true,
		"providers": true, "hide_icon": true, "uncompressed_size": true, "single": true,
		"deprecated": true, "variant_of": true, "conflicts_with": true, "sdk": true,
		"assets": true,
	}
	for idx, rawPlugin := range plugins {
		var fields map[string]json.RawMessage
		if json.Unmarshal(rawPlugin, &fields) != nil {
			continue
		}
		for _, key := range sortedKeys(fields) {
			if !knownPlugin[key] {
				issues = append(issues, Issue{
					Code: "plugin.repo.unknown_field", Severity: SeverityWarn,
					Path:   fmt.Sprintf("plugins[%d].%s", idx, key),
					Params: map[string]any{"field": key},
				})
			}
		}
	}
	return issues
}

func sortedKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
