//go:build ignore

// pluginctl 是插件仓库的构建与索引工具。
//
// 用法：
//
//	go run scripts/pluginctl manifest   # 生成 plugins.json（内部版本）
//	go run scripts/pluginctl manifest -version 1.3.4  # 部署/通知等合并包统一使用 tag 版本
//	go run scripts/pluginctl gen        # 校验合并包清单
//	go run scripts/pluginctl matrix     # 输出 CI 矩阵 JSON
//	go run scripts/pluginctl build -kind dns -id cloudflare -version 1.0.0 -goos linux -goarch amd64
//	go run scripts/pluginctl index -assets dist -base-url https://.../releases/download -valid-for 720h -out index.json
//	go run scripts/pluginctl validate -in index.json
//
// 索引签名请使用独立命令 go run ./tools/helpers/signer，避免签名进程导入/执行插件代码。
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	notify "lactssl/internal/plugin/host/notify"
	"lactssl/internal/plugin/scripts/buildver"
	"lactssl/internal/plugin/scripts/indexschema"
	"lactssl/providers/catalog"
	dnsv5meta "lactssl/providers/dns/lego5/meta"
)

const (
	manifestPath = "plugins.json"
	// 主/次版本由人工维护；补丁号由 manifest 根据各合并包自身的输入内容自动递增。
	// 每个包独立计数：只有自己的源码/图标等输入变化时才升版，共享目录（protocol、sdk、
	// i18n 等）的改动不再同时触发所有包升版。
	// 上游版本跟随的包（如 all-lego-v5）不走这套版本计算。
	deployBaseVer = "1.0"
	notifyBaseVer = "1.0"
	legoV5Module  = "github.com/go-acme/lego/v5"
)

type pluginEntry struct {
	Kind          string   `json:"kind"`
	ID            string   `json:"id"`
	Version       string   `json:"version"`
	Tag           string   `json:"tag"`
	MinHost       string   `json:"min_host,omitempty"`
	Name          string   `json:"name,omitempty"`
	Description   string   `json:"description,omitempty"`
	Bundle        bool     `json:"bundle,omitempty"`
	Providers     []string `json:"providers,omitempty"`
	VariantOf     string   `json:"variant_of,omitempty"`
	ConflictsWith []string `json:"conflicts_with,omitempty"`
	HideIcon      bool     `json:"hide_icon,omitempty"`
	SDK           string   `json:"sdk,omitempty"`
	// SourceHash 是该合并包输入文件的内容指纹，用于自动递增补丁版本。
	SourceHash string `json:"source_hash,omitempty"`

	// baseVersion 是主/次版本基线，只用于 manifest 计算，不写入 plugins.json。
	baseVersion string
	// upstreamModule 非空时，版本号直接使用 go.mod 中该模块的版本（去掉 v 前缀），
	// 不参与内容指纹递增，也不会被 tag 发布版本覆盖。
	upstreamModule string
}

type manifestFile struct {
	Schema  int           `json:"schema"`
	Plugins []pluginEntry `json:"plugins"`
}

type assetMeta struct {
	Kind             string   `json:"kind"`
	SourceHash       string   `json:"source_hash,omitempty"`
	ID               string   `json:"id"`
	Version          string   `json:"version"`
	Tag              string   `json:"tag"`
	Name             string   `json:"name,omitempty"`
	Description      string   `json:"description,omitempty"`
	Bundle           bool     `json:"bundle,omitempty"`
	Providers        []string `json:"providers,omitempty"`
	VariantOf        string   `json:"variant_of,omitempty"`
	ConflictsWith    []string `json:"conflicts_with,omitempty"`
	HideIcon         bool     `json:"hide_icon,omitempty"`
	SDK              string   `json:"sdk,omitempty"`
	OS               string   `json:"os"`
	Arch             string   `json:"arch"`
	Asset            string   `json:"asset"`
	SHA256           string   `json:"sha256"`
	Size             int64    `json:"size"`
	UncompressedSize int64    `json:"uncompressed_size,omitempty"`
}

type indexFile struct {
	Schema      int           `json:"schema"`
	Channel     string        `json:"channel"`
	Protocol    string        `json:"protocol"`
	Revision    uint64        `json:"revision,omitempty"`
	GeneratedAt string        `json:"generated_at"`
	ExpiresAt   string        `json:"expires_at,omitempty"`
	Plugins     []indexPlugin `json:"plugins"`
	Icons       []indexIcon   `json:"icons,omitempty"`
}

// iconFileRef 指向插件包内的图标文件。
type iconFileRef struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

// iconAsset 是仓库 assets/icons 下的一张图标。
type iconAsset struct {
	Kind    string
	ID      string
	Ext     string
	MIME    string
	SHA256  string
	Data    []byte
	Source  string
	Aliases []string
}

// iconManifest 是 assets/icons/icons.json，按 kind 声明复用关系。
type iconManifest struct {
	Schema  int                          `json:"schema"`
	Aliases map[string]map[string]string `json:"aliases"`
}

// indexIcon 是索引中的内联图标，kind/id 为规范 ID，aliases 为历史 ID。
type indexIcon struct {
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	MIME    string   `json:"mime"`
	SHA256  string   `json:"sha256"`
	Data    string   `json:"data"`
	Aliases []string `json:"aliases,omitempty"`
}

const (
	iconRoot     = "assets/icons"
	maxIconBytes = 64 << 10
)

var iconIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

var iconMIME = map[string]string{
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".webp": "image/webp",
}

type indexPlugin struct {
	Kind             string                `json:"kind"`
	SourceHash       string                `json:"source_hash,omitempty"`
	ID               string                `json:"id"`
	Name             string                `json:"name,omitempty"`
	Description      string                `json:"description,omitempty"`
	Version          string                `json:"version"`
	Protocol         string                `json:"protocol"`
	MinHost          string                `json:"min_host,omitempty"`
	Bundle           bool                  `json:"bundle,omitempty"`
	Providers        []string              `json:"providers,omitempty"`
	VariantOf        string                `json:"variant_of,omitempty"`
	ConflictsWith    []string              `json:"conflicts_with,omitempty"`
	HideIcon         bool                  `json:"hide_icon,omitempty"`
	SDK              string                `json:"sdk,omitempty"`
	UncompressedSize int64                 `json:"uncompressed_size,omitempty"`
	Single           bool                  `json:"single,omitempty"`
	Deprecated       bool                  `json:"deprecated,omitempty"`
	Assets           map[string]indexAsset `json:"assets"`
}

type indexAsset struct {
	URL              string `json:"url"`
	SHA256           string `json:"sha256"`
	Size             int64  `json:"size"`
	UncompressedSize int64  `json:"uncompressed_size,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fail("用法: pluginctl manifest|gen|matrix|build|index")
	}
	switch os.Args[1] {
	case "manifest":
		cmdManifest(os.Args[2:])
	case "validate":
		cmdValidate(os.Args[2:])
	case "gen":
		cmdGen()
	case "matrix":
		cmdMatrix()
	case "build":
		cmdBuild(os.Args[2:])
	case "index":
		cmdIndex(os.Args[2:])
	default:
		fail("未知子命令: " + os.Args[1])
	}
}

func cmdManifest(args []string) {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	version := fs.String("version", "", "合并包统一使用的发布版本（tag 构建；跟随上游版本的包除外）")
	fs.Parse(args)

	entries := buildEntries()

	previous := map[string]pluginEntry{}
	if old, err := readManifest(); err == nil {
		for _, entry := range old {
			previous[entry.Kind+"|"+entry.ID] = entry
		}
	} else if !os.IsNotExist(err) {
		fail(err.Error())
	}

	for i := range entries {
		hash, err := buildver.HashPaths(".", sourcePaths(entries[i]))
		if err != nil {
			fail("计算 " + entries[i].Kind + "/" + entries[i].ID + " 内容指纹失败: " + err.Error())
		}
		entries[i].SourceHash = hash
		if entries[i].upstreamModule != "" {
			v, err := moduleVersion(entries[i].upstreamModule)
			if err != nil {
				fail("读取 " + entries[i].upstreamModule + " 版本失败: " + err.Error())
			}
			entries[i].Version = v
			continue
		}
		if old, ok := previous[entries[i].Kind+"|"+entries[i].ID]; ok {
			entries[i].Version = buildver.Next(old.Version, old.SourceHash, entries[i].baseVersion, hash)
		}
	}

	if raw := strings.TrimSpace(*version); raw != "" {
		tagVersion := strings.TrimPrefix(raw, "v")
		if !versionPattern.MatchString(tagVersion) {
			fail("发布版本格式不合法: " + *version)
		}
		for i := range entries {
			// 跟随上游版本的包不使用仓库 tag 版本。
			if entries[i].upstreamModule != "" {
				continue
			}
			entries[i].Version = tagVersion
		}
	}

	for _, entry := range entries {
		if !versionPattern.MatchString(entry.Version) {
			fail("插件版本格式不合法: " + entry.Kind + "/" + entry.ID + " " + entry.Version)
		}
	}
	sortEntries(entries)
	out := manifestFile{Schema: 2, Plugins: entries}
	writeJSON(manifestPath, out)
	fmt.Printf("wrote %s with %d bundles\n", manifestPath, len(entries))
}

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([.+-][0-9A-Za-z.-]+)?$`)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var tagPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// sourcePaths 返回影响某个合并包产物的输入。
// 公共构建输入（go.mod/go.sum、许可证、图标别名表、协议、SDK、i18n 运行时、
// outbound 与生成的 catalog 聚合）会编进每个 helper，因此变化时所有由内容
// 指纹管理的包都会升版；provider 自身的源码仍只影响对应类型的包。
// 这样避免“版本相同但二进制内容不同”，代价是共享代码改动会触发多个包升版。
func sourcePaths(entry pluginEntry) []string {
	paths := []string{
		"../../go.mod",
		"../../go.sum",
		"Makefile",
		"scripts",
		"LICENSE",
		"assets/icons/icons.json",
		"../../plugins/protocol",
		"../../plugins/sdk",
		"../../plugins/outbound",
		"../../plugins/i18n",
		"providers/catalog",
		"providers/go.mod",
		"providers/go.sum",
	}
	if entry.SDK == "lego-v5" {
		// v5 helper 依赖 dns 的接口/注册表和跨进程 recordID 状态包。
		return append(paths, "tools/helpers/dns-helper-v5", "providers/dns/lego5", "host/dns")
	}
	switch entry.Kind {
	case "deploy":
		return append(paths, "tools/helpers/deploy-helper", "host/deploy", "providers/deploy", "assets/icons/deploy")
	case "notify":
		return append(paths, "tools/helpers/notify-helper", "host/notify", "providers/notify", "assets/icons/notify")
	}
	return paths
}

// buildEntries 生成合并插件包：DNS 只发布 lego v5 变体，部署与通知各一个包。
// v4 的 all-lego / all-native 源码与本地回归入口已移除。
func buildEntries() []pluginEntry {
	deployIDs := make([]string, 0, 8)
	for _, p := range catalog.Deployers() {
		if p.Deprecated {
			continue
		}
		deployIDs = append(deployIDs, p.Value)
	}
	notifyIDs := notify.OfficialChannelIDs()
	v5IDs := dnsv5meta.IDs()
	return []pluginEntry{
		{
			Kind: "dns", ID: "all-lego-v5", Version: "0.0.0", Tag: "bundle_dns_all_lego_v5",
			Name: "DNS 插件包（全量·lego v5）", Description: "228 个 lego v5 上游服务商，另保留 7 个已移除 ID 的兼容占位。",
			Bundle: true, Providers: v5IDs, HideIcon: true, SDK: "lego-v5",
			// 保留旧包 ID 的冲突声明，便于已安装 all-lego / all-native 的宿主在升级时回收旧 helper。
			ConflictsWith:  []string{"all-lego", "all-native"},
			upstreamModule: legoV5Module,
		},
		{
			Kind: "deploy", ID: "all", Version: deployBaseVer + ".0", Tag: "bundle_deploy_all",
			Name: "部署插件包", Description: "包含全部官方证书部署目标实现。",
			Bundle: true, Providers: deployIDs, baseVersion: deployBaseVer,
		},
		{
			Kind: "notify", ID: "all", Version: notifyBaseVer + ".0", Tag: "bundle_notify_all",
			Name: "通知插件包", Description: "包含全部官方通知渠道实现。",
			Bundle: true, Providers: notifyIDs, baseVersion: notifyBaseVer,
		},
	}
}

func sortEntries(entries []pluginEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].ID < entries[j].ID
	})
}

func readManifest() ([]pluginEntry, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var m manifestFile
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("解析 plugins.json 失败: %w", err)
	}
	return m.Plugins, nil
}

func loadManifest() []pluginEntry {
	entries, err := readManifest()
	if err != nil {
		fail("读取 plugins.json 失败: " + err.Error())
	}
	return entries
}

// moduleVersion 从 go.mod 读取指定模块的版本，并去掉 semver 的 v 前缀。
// 只处理直接 require；replace / exclude 行会被忽略。
func moduleVersion(module string) (string, error) {
	raw, err := os.ReadFile("../../go.mod")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") || strings.Contains(line, "=>") {
			continue
		}
		fields := strings.Fields(line)
		switch {
		case len(fields) >= 2 && fields[0] == module && strings.HasPrefix(fields[1], "v"):
			return strings.TrimPrefix(fields[1], "v"), nil
		case len(fields) >= 3 && fields[0] == "require" && fields[1] == module && strings.HasPrefix(fields[2], "v"):
			return strings.TrimPrefix(fields[2], "v"), nil
		}
	}
	return "", fmt.Errorf("go.mod 中没有找到 %s 的版本", module)
}

func cmdMatrix() {
	entries := loadManifest()
	writeJSONTo(os.Stdout, entries)
	fmt.Println()
}

// cmdGen 保留历史子命令以兼容 make manifest。v5 helper 直接导入
// providers/dns/lego5/groups/all，不再生成 v4 分组或 tools/helpers/dns-helper 选择器。
func cmdGen() {
	for _, entry := range loadManifest() {
		if entry.Kind == "dns" && entry.Bundle && entry.SDK != "lego-v5" {
			fail("不再支持 v4 DNS 合并包: " + entry.ID)
		}
	}
	fmt.Println("no generated DNS groups (v5 uses providers/dns/lego5/groups/all)")
}

// entryProviders 返回插件包覆盖的 provider 集合；单插件包没有 providers 时用自身 ID。
func entryProviders(entry pluginEntry) []string {
	if len(entry.Providers) > 0 {
		return entry.Providers
	}
	return []string{entry.ID}
}

// loadIcons 读取 assets/icons 下的全部图标，并展开 icons.json 中的别名。
func loadIcons() (map[string]map[string]*iconAsset, error) {
	icons := map[string]map[string]*iconAsset{}
	manifest := iconManifest{}
	manifestPath := filepath.Join(iconRoot, "icons.json")
	if raw, err := os.ReadFile(manifestPath); err == nil {
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", manifestPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, kind := range []string{"dns", "deploy", "notify"} {
		dir := filepath.Join(iconRoot, kind)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			ext := strings.ToLower(filepath.Ext(name))
			mime, ok := iconMIME[ext]
			if !ok {
				return nil, fmt.Errorf("图标 %s 的格式不受支持（仅支持 svg/png/webp）", name)
			}
			id := strings.TrimSuffix(name, filepath.Ext(name))
			if !iconIDPattern.MatchString(id) {
				return nil, fmt.Errorf("图标 ID 非法: %q", id)
			}
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("图标必须是普通文件: %s", name)
			}
			if info.Size() > maxIconBytes {
				return nil, fmt.Errorf("图标 %s 超过 %d 字节", name, maxIconBytes)
			}
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, err
			}
			if icons[kind] == nil {
				icons[kind] = map[string]*iconAsset{}
			}
			if _, dup := icons[kind][id]; dup {
				return nil, fmt.Errorf("图标重复: %s/%s", kind, id)
			}
			sum := sha256.Sum256(data)
			icons[kind][id] = &iconAsset{
				Kind: kind, ID: id, Ext: ext, MIME: mime,
				SHA256: hex.EncodeToString(sum[:]), Data: data,
				Source: filepath.Join(dir, name),
			}
		}
	}
	for kind, aliases := range manifest.Aliases {
		for alias, target := range aliases {
			if !iconIDPattern.MatchString(alias) {
				return nil, fmt.Errorf("图标别名非法: %q", alias)
			}
			asset, ok := icons[kind][target]
			if !ok {
				return nil, fmt.Errorf("图标别名 %s/%s 指向不存在的图标 %q", kind, alias, target)
			}
			asset.Aliases = append(asset.Aliases, alias)
		}
	}
	for _, byID := range icons {
		for _, asset := range byID {
			sort.Strings(asset.Aliases)
		}
	}
	return icons, nil
}

func mustLoadIcons() map[string]map[string]*iconAsset {
	icons, err := loadIcons()
	if err != nil {
		fail(err.Error())
	}
	return icons
}

// buildIndexIcons 只为 plugins.json 中仍存在的 provider 输出图标。
func buildIndexIcons(icons map[string]map[string]*iconAsset, entries []pluginEntry) []indexIcon {
	wanted := map[string]bool{}
	for _, entry := range entries {
		for _, provider := range entryProviders(entry) {
			wanted[entry.Kind+"|"+provider] = true
		}
	}
	out := make([]indexIcon, 0)
	for kind, byID := range icons {
		for id, asset := range byID {
			if !wanted[kind+"|"+id] {
				continue
			}
			out = append(out, indexIcon{
				Kind: kind, ID: id, MIME: asset.MIME, SHA256: asset.SHA256,
				Data:    "data:" + asset.MIME + ";base64," + base64.StdEncoding.EncodeToString(asset.Data),
				Aliases: asset.Aliases,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func cmdBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	kind := fs.String("kind", "", "dns|deploy|notify")
	id := fs.String("id", "", "plugin id")
	version := fs.String("version", "", "plugin version")
	goos := fs.String("goos", runtime.GOOS, "target os")
	goarch := fs.String("goarch", runtime.GOARCH, "target arch")
	outdir := fs.String("out", "dist", "output directory")
	fs.Parse(args)
	if *kind == "" || *id == "" {
		fail("build 需要 -kind 与 -id")
	}
	entry, ok := findEntryByID(loadManifest(), *kind, *id)
	if !ok {
		fail("plugins.json 中找不到插件: " + *kind + "/" + *id)
	}
	if *version == "" {
		*version = entry.Version
	}
	asset, meta, err := buildPlugin(entry, *version, *goos, *goarch, *outdir)
	if err != nil {
		fail(err.Error())
	}
	fmt.Printf("built %s (compressed=%d bytes, uncompressed=%d bytes, sha256=%s)\n", asset, meta.Size, meta.UncompressedSize, meta.SHA256)
}

func findEntryByID(entries []pluginEntry, kind, id string) (pluginEntry, bool) {
	for _, e := range entries {
		if e.Kind == kind && e.ID == id {
			return e, true
		}
	}
	return pluginEntry{}, false
}

func buildPlugin(entry pluginEntry, version, goos, goarch, outdir string) (string, assetMeta, error) {
	if !versionPattern.MatchString(version) {
		return "", assetMeta{}, fmt.Errorf("版本格式不合法: %q", version)
	}
	if !idPattern.MatchString(entry.ID) || (entry.Kind != "dns" && entry.Kind != "deploy" && entry.Kind != "notify") {
		return "", assetMeta{}, fmt.Errorf("插件 kind/id 非法: %s/%s", entry.Kind, entry.ID)
	}
	if strings.ContainsAny(goos+goarch, `/\\`) {
		return "", assetMeta{}, fmt.Errorf("目标平台非法: %s/%s", goos, goarch)
	}
	name := "lactssl-" + entry.Kind + "-" + entry.ID
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	if err := os.MkdirAll(outdir, 0o755); err != nil {
		return "", assetMeta{}, err
	}
	tmp, err := os.MkdirTemp(outdir, ".build-*")
	if err != nil {
		return "", assetMeta{}, err
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, name+ext)
	ldflags := "-s -w -X main.buildVersion=" + version
	tags := entry.Tag
	if tags == "" {
		tags = "bundle_" + entry.Kind + "_all"
	}
	if !strings.Contains(tags, "nethttpomithttp2") {
		tags += ",nethttpomithttp2"
	}
	// Go 1.27 的 net/http 与新版 x/net/http2 的包装层存在兼容问题，
	// 使用 x/net 提供的 legacy HTTP/2 实现，避免 Azure SDK 初始化时 panic。
	if !strings.Contains(tags, "http2legacy") {
		tags += ",http2legacy"
	}
	buildDir := "./cmd/" + entry.Kind + "-helper"
	if entry.SDK == "lego-v5" {
		buildDir = "./tools/helpers/dns-helper-v5"
		// 与 lego v5 上游 release 一致，裁掉 validator/ozzo/urfave 的模板代码。
		// v5 provider 依赖会引入这些包，实测可减少约 23MB。
		tags += ",urfave_cli_no_template,ozzo_validation_no_template,grpcnotrace,validator_novalidatefn"
	}
	cmd := exec.Command("go", "build", "-trimpath", "-tags", tags, "-ldflags", ldflags, "-o", bin, buildDir)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", assetMeta{}, fmt.Errorf("构建 %s/%s 失败: %v\n%s", entry.Kind, entry.ID, err, out)
	}
	if runtime.GOOS == goos && runtime.GOARCH == goarch {
		// 在一个隔离副本、最小环境和临时工作目录中执行 manifest 探测；
		// 探测进程不继承 CI token/代理/云凭据，且探测后校验原始二进制未被篡改。
		// outdir 可能是相对路径（发布 workflow 使用 -out dist），而 probeCmd.Dir 会先
		// 切换子进程工作目录再 exec；因此探测路径与工作目录都必须转成绝对路径，
		// 否则相对路径会被二次解析成 dist/.build-*/dist/.build-*/probe。
		probeDir, err := filepath.Abs(tmp)
		if err != nil {
			return "", assetMeta{}, fmt.Errorf("%s 解析探测目录失败: %v", name, err)
		}
		probePath := filepath.Join(probeDir, "probe"+ext)
		if err := copyFile(bin, probePath, 0o755); err != nil {
			return "", assetMeta{}, fmt.Errorf("%s 创建探测副本失败: %v", name, err)
		}
		before, _, err := fileDigest(bin)
		if err != nil {
			return "", assetMeta{}, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		probeCmd := exec.CommandContext(ctx, probePath, "manifest")
		probeCmd.Dir = probeDir
		probeCmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + probeDir,
			"TMPDIR=" + probeDir,
			"LACTSSL_DEPLOY_PLUGIN_FEATURES=merged-deployers-v1",
		}
		out, err := probeCmd.Output()
		if err != nil {
			return "", assetMeta{}, fmt.Errorf("%s manifest 校验失败: %v", name, err)
		}
		after, _, err := fileDigest(bin)
		if err != nil {
			return "", assetMeta{}, err
		}
		if before != after {
			return "", assetMeta{}, fmt.Errorf("%s 二进制在 manifest 探测期间被修改", name)
		}
		var probe struct {
			Providers []struct{ ID string } `json:"providers"`
			Deployers []struct{ ID string } `json:"deployers"`
			Channels  []struct{ ID string } `json:"channels"`
			Version   string                `json:"version"`
		}
		if err := json.Unmarshal(out, &probe); err != nil {
			return "", assetMeta{}, fmt.Errorf("%s manifest 不是合法 JSON: %v", name, err)
		}
		items := len(probe.Providers) + len(probe.Deployers) + len(probe.Channels)
		if items == 0 {
			return "", assetMeta{}, fmt.Errorf("%s manifest 没有包含任何条目", name)
		}
		if entry.Bundle && len(entry.Providers) > 0 {
			got := make([]string, 0, items)
			for _, item := range probe.Providers {
				got = append(got, item.ID)
			}
			for _, item := range probe.Deployers {
				got = append(got, item.ID)
			}
			for _, item := range probe.Channels {
				got = append(got, item.ID)
			}
			sort.Strings(got)
			want := append([]string(nil), entry.Providers...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				return "", assetMeta{}, fmt.Errorf("%s manifest 条目与索引不一致: 期望 %d 个，实际 %d 个", name, len(want), len(got))
			}
		}
		if probe.Version != version {
			return "", assetMeta{}, fmt.Errorf("%s manifest 版本 %q 与索引版本 %q 不一致", name, probe.Version, version)
		}
	}

	pluginJSON := map[string]any{
		"schema":         1,
		"kind":           entry.Kind,
		"id":             entry.ID,
		"version":        version,
		"protocol":       "v1",
		"os":             goos,
		"arch":           goarch,
		"entrypoint":     name + ext,
		"bundle":         entry.Bundle,
		"providers":      entry.Providers,
		"variant_of":     entry.VariantOf,
		"conflicts_with": entry.ConflictsWith,
		"hide_icon":      entry.HideIcon,
		"sdk":            entry.SDK,
		"source_hash":    entry.SourceHash,
	}
	pluginRaw, _ := json.MarshalIndent(pluginJSON, "", "  ")
	icons := mustLoadIcons()
	refs := map[string]iconFileRef{}
	files := map[string]fileEntry{
		"plugin.json": {data: append(pluginRaw, '\n'), mode: 0o644},
		name + ext:    {path: bin, mode: 0o755},
		"LICENSE":     {path: "LICENSE", mode: 0o644},
	}
	for _, provider := range entryProviders(entry) {
		asset, found := icons[entry.Kind][provider]
		if !found {
			continue
		}
		iconPath := "icons/" + provider + asset.Ext
		files[iconPath] = fileEntry{path: asset.Source, mode: 0o644}
		refs[provider] = iconFileRef{File: iconPath, SHA256: asset.SHA256}
	}
	if len(refs) > 0 {
		pluginJSON["icons"] = refs
		pluginRaw, _ = json.MarshalIndent(pluginJSON, "", "  ")
		files["plugin.json"] = fileEntry{data: append(pluginRaw, '\n'), mode: 0o644}
	}
	assetName := fmt.Sprintf("%s_%s_%s_%s.tar.gz", name, version, goos, goarch)
	assetPath := filepath.Join(outdir, assetName)
	if err := writeTarGz(assetPath, files); err != nil {
		return "", assetMeta{}, err
	}
	digest, size, err := fileDigest(assetPath)
	if err != nil {
		return "", assetMeta{}, err
	}
	info, err := os.Stat(bin)
	if err != nil {
		return "", assetMeta{}, err
	}
	meta := assetMeta{
		Kind: entry.Kind, ID: entry.ID, Version: version, Tag: entry.Tag,
		SourceHash: entry.SourceHash,
		Name:       entry.Name, Description: entry.Description, Bundle: entry.Bundle, Providers: entry.Providers,
		VariantOf: entry.VariantOf, ConflictsWith: entry.ConflictsWith, SDK: entry.SDK,
		HideIcon: entry.HideIcon,
		OS:       goos, Arch: goarch, Asset: assetName, SHA256: digest, Size: size,
		UncompressedSize: info.Size(),
	}
	metaPath := strings.TrimSuffix(assetPath, ".tar.gz") + ".meta.json"
	writeJSON(metaPath, meta)
	return assetPath, meta, nil
}

type fileEntry struct {
	path string
	data []byte
	mode int64
}

func writeTarGz(dest string, files map[string]fileEntry) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	gz, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := files[name]
		data := entry.data
		if entry.path != "" {
			data, err = os.ReadFile(entry.path)
			if err != nil {
				return err
			}
		}
		header := &tar.Header{Name: name, Mode: entry.mode, Size: int64(len(data)), ModTime: time.Unix(0, 0)}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func fileDigest(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func cmdIndex(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	assets := fs.String("assets", "dist", "directory containing *.meta.json")
	baseURL := fs.String("base-url", "", "release download base URL")
	bucket := fs.String("bucket", "", "release 名前缀：URL 为 <base-url>/<bucket>-<kind>/<asset>；留空按 <kind>-<id>-v<version>")
	prev := fs.String("prev", "", "已有 index.json，用于合并未重建的插件")
	allowDowngrade := fs.Bool("allow-downgrade", false, "允许新索引版本低于旧索引（用于显式重置插件版本）")
	requireComplete := fs.Bool("require-complete", false, "要求索引覆盖 plugins.json 中的全部插件（发布使用）")
	skipSchema := fs.Bool("skip-schema", false, "跳过 JSON Schema 结构校验（仅用于紧急排障）")
	validFor := fs.Duration("valid-for", 30*24*time.Hour, "索引有效期；0 表示不写 expires_at")
	out := fs.String("out", "index.json", "output index path")
	fs.Parse(args)
	if *baseURL == "" {
		fail("index 需要 -base-url")
	}
	if *validFor < 0 {
		fail("-valid-for 不能为负数")
	}
	metas, err := filepath.Glob(filepath.Join(*assets, "*.meta.json"))
	if err != nil {
		fail(err.Error())
	}
	type group struct {
		meta   assetMeta
		assets map[string]indexAsset
	}
	groups := map[string]map[string]*group{}
	for _, path := range metas {
		raw, err := os.ReadFile(path)
		if err != nil {
			fail(err.Error())
		}
		var meta assetMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			fail(err.Error())
		}
		if meta.Kind != "dns" && meta.Kind != "deploy" && meta.Kind != "notify" {
			fail("meta kind 非法: " + meta.Kind)
		}
		if !idPattern.MatchString(meta.ID) {
			fail("meta ID 非法: " + meta.ID)
		}
		if !versionPattern.MatchString(meta.Version) {
			fail("meta 版本非法: " + meta.Version)
		}
		if meta.Asset == "" || strings.ContainsAny(meta.Asset, `/\`) || strings.Contains(meta.Asset, "..") {
			fail("meta 资产名非法: " + meta.Asset)
		}
		if !idPattern.MatchString(meta.OS) || !idPattern.MatchString(meta.Arch) {
			fail("meta 平台非法: " + meta.OS + "/" + meta.Arch)
		}
		if len(meta.SHA256) != 64 {
			fail("meta sha256 非法: " + meta.SHA256)
		}
		if _, err := hex.DecodeString(meta.SHA256); err != nil {
			fail("meta sha256 非法: " + meta.SHA256)
		}
		key := meta.Kind + "|" + meta.ID
		if groups[key] == nil {
			groups[key] = map[string]*group{}
		}
		item := groups[key][meta.Version]
		if item == nil {
			item = &group{meta: meta, assets: map[string]indexAsset{}}
			groups[key][meta.Version] = item
		}
		releaseTag := fmt.Sprintf("%s-%s-v%s", meta.Kind, meta.ID, meta.Version)
		if *bucket != "" {
			releaseTag = *bucket + "-" + meta.Kind
		}
		item.assets[meta.OS+"/"+meta.Arch] = indexAsset{
			URL:              strings.TrimRight(*baseURL, "/") + "/" + releaseTag + "/" + meta.Asset,
			SHA256:           meta.SHA256,
			Size:             meta.Size,
			UncompressedSize: meta.UncompressedSize,
		}
	}
	plugins := make([]indexPlugin, 0, len(groups))
	for _, versions := range groups {
		var latest *group
		for _, item := range versions {
			if latest == nil || compareVersion(item.meta.Version, latest.meta.Version) > 0 {
				latest = item
			}
		}
		if latest == nil {
			continue
		}
		meta := latest.meta
		name, desc := meta.Name, meta.Description
		if name == "" || desc == "" {
			if def, ok := lookupMeta(meta.Kind, meta.ID); ok {
				if name == "" {
					name = def.name
				}
				if desc == "" {
					desc = def.desc
				}
			}
		}
		hideIcon := meta.HideIcon
		if !hideIcon {
			if entry, ok := findEntryByID(loadManifest(), meta.Kind, meta.ID); ok {
				hideIcon = entry.HideIcon
			}
		}
		plugins = append(plugins, indexPlugin{
			Kind: meta.Kind, ID: meta.ID, SourceHash: meta.SourceHash, Name: name, Description: desc,
			Version: meta.Version, Protocol: "v1", Bundle: meta.Bundle,
			Providers: meta.Providers, VariantOf: meta.VariantOf, ConflictsWith: meta.ConflictsWith,
			HideIcon: hideIcon, SDK: meta.SDK, UncompressedSize: meta.UncompressedSize,
			Single: !meta.Bundle, Assets: latest.assets,
		})
	}
	prevIndex, hasPrev := loadPrevIndex(*prev)
	now := time.Now().UTC()
	index := indexFile{
		Schema: 1, Channel: "stable", Protocol: "v1",
		Revision: nextRevision(prevIndex, hasPrev), GeneratedAt: now.Format(time.RFC3339),
		Plugins: plugins,
	}
	if *validFor > 0 {
		index.ExpiresAt = now.Add(*validFor).UTC().Format(time.RFC3339)
	}
	if hasPrev {
		plugins = mergeIndex(plugins, prevIndex, *allowDowngrade)
		index.Plugins = plugins
	}
	if *requireComplete {
		expected := map[string]bool{}
		for _, entry := range loadManifest() {
			expected[entry.Kind+"|"+entry.ID] = true
		}
		seen := map[string]bool{}
		for _, plugin := range index.Plugins {
			key := plugin.Kind + "|" + plugin.ID
			if !expected[key] {
				fail("索引包含 plugins.json 中不存在的插件: " + key)
			}
			if seen[key] {
				fail("索引包含重复插件: " + key)
			}
			seen[key] = true
		}
		for key := range expected {
			if !seen[key] {
				fail("索引缺少 plugins.json 中的插件: " + key)
			}
		}
	}
	sort.Slice(index.Plugins, func(i, j int) bool {
		a, b := index.Plugins[i], index.Plugins[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	index.Icons = buildIndexIcons(mustLoadIcons(), loadManifest())
	if !*skipSchema {
		raw, err := json.Marshal(index)
		if err != nil {
			fail("编码索引失败: " + err.Error())
		}
		if err := indexschema.Validate(raw); err != nil {
			fail("索引 JSON Schema 校验失败: " + err.Error())
		}
	}
	writeJSON(*out, index)
	fmt.Printf("wrote %s with %d plugins and %d icons\n", *out, len(index.Plugins), len(index.Icons))
}

// cmdValidate 校验已有索引文件的结构契约，发布流程在签名前调用。
func cmdValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	in := fs.String("in", "index.json", "待校验的索引文件")
	fs.Parse(args)
	raw, err := os.ReadFile(*in)
	if err != nil {
		fail("读取索引失败: " + err.Error())
	}
	if err := indexschema.Validate(raw); err != nil {
		fail("索引 JSON Schema 校验失败: " + err.Error())
	}
	fmt.Printf("%s: index schema valid\n", *in)
}

// loadPrevIndex 读取上一份索引。文件不存在表示首次发布，不视为错误。
func loadPrevIndex(path string) (indexFile, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return indexFile{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return indexFile{}, false
		}
		fail("读取旧索引失败: " + err.Error())
	}
	var old indexFile
	if err := json.Unmarshal(raw, &old); err != nil {
		fail("解析旧索引失败: " + err.Error())
	}
	return old, true
}

// nextRevision 生成单调递增的索引版本。旧索引没有 revision 时从 1 开始，
// 兼容本次改造前已经发布的 schema 1 索引。
func nextRevision(prev indexFile, have bool) uint64 {
	if !have || prev.Revision == 0 {
		return 1
	}
	if prev.Revision == ^uint64(0) {
		fail("旧索引 revision 已达上限")
	}
	return prev.Revision + 1
}

// mergeIndex 把未参与本次构建的插件从旧索引沿用过来。
// 已被重新构建的插件以新结果为准，默认不会把版本降级（避免非 tag 发布盖掉 tag 版本）；
// allowDowngrade 为 true 时允许显式重置版本后覆盖旧索引。
// plugins.json 中已删除的插件不再保留。
func mergeIndex(fresh []indexPlugin, old indexFile, allowDowngrade bool) []indexPlugin {
	merged := map[string]indexPlugin{}
	for _, p := range fresh {
		merged[p.Kind+"|"+p.ID] = p
	}
	for _, p := range old.Plugins {
		key := p.Kind + "|" + p.ID
		if cur, ok := merged[key]; ok {
			if !allowDowngrade && compareVersion(cur.Version, p.Version) < 0 {
				merged[key] = p
			}
			continue
		}
		if !inManifest(p.Kind, p.ID) {
			continue
		}
		merged[key] = p
	}
	out := make([]indexPlugin, 0, len(merged))
	for _, p := range merged {
		out = append(out, p)
	}
	return out
}

func inManifest(kind, id string) bool {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return true
	}
	var m manifestFile
	if err := json.Unmarshal(raw, &m); err != nil {
		return true
	}
	for _, e := range m.Plugins {
		if e.Kind == kind && e.ID == id {
			return true
		}
	}
	return false
}

type metaInfo struct{ name, desc string }

func lookupMeta(kind, id string) (metaInfo, bool) {
	switch kind {
	case "dns":
		if p, ok := catalog.LookupDNS(id); ok {
			return metaInfo{name: p.LocalizedName(i18n.Default), desc: p.LocalizedDesc(i18n.Default)}, true
		}
	case "deploy":
		if p, ok := catalog.LookupDeployer(id); ok {
			return metaInfo{name: p.LocalizedName(i18n.Default), desc: p.LocalizedDesc(i18n.Default)}, true
		}
	case "notify":
		return metaInfo{name: id, desc: ""}, true
	}
	return metaInfo{}, false
}

func writeJSON(path string, v any) {
	f, err := os.Create(path)
	if err != nil {
		fail(err.Error())
	}
	defer f.Close()
	writeJSONTo(f, v)
	fmt.Fprintln(f)
}

func writeJSONTo(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fail(err.Error())
	}
}

func compareVersion(a, b string) int {
	parse := func(v string) ([3]int, string) {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		pre := ""
		if idx := strings.IndexAny(v, "-+"); idx >= 0 {
			pre = v[idx+1:]
			v = v[:idx]
		}
		var out [3]int
		for i, part := range strings.SplitN(v, ".", 3) {
			out[i], _ = strconv.Atoi(part)
		}
		return out, pre
	}
	ca, pa := parse(a)
	cb, pb := parse(b)
	for i := 0; i < 3; i++ {
		if ca[i] > cb[i] {
			return 1
		}
		if ca[i] < cb[i] {
			return -1
		}
	}
	if pa == "" && pb == "" {
		return 0
	}
	if pa == "" {
		return 1
	}
	if pb == "" {
		return -1
	}
	if pa > pb {
		return 1
	}
	if pa < pb {
		return -1
	}
	return 0
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
