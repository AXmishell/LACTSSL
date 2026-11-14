//go:build ignore

// gen_dns_plugins 生成 DNS 服务商插件包装包与构建 profile。
//
// 用法：
//
//	go run scripts/gen_dns_plugins/main.go
//
// 生成内容：
//   - internal/dnsplugin/providers/<id>/plugin.go：每个 lego DNS 服务商的薄包装包
//   - internal/dnsplugin/builtin/profile_lite.go：默认构建包含的服务商集合（默认空）
//   - internal/dnsplugin/builtin/profile_extra.go：dns_extra 构建标签下的插件集合（非精简）
//   - internal/dnsplugin/builtin/profile_full.go：dns_all 构建标签下的全量集合
//
// 精简集合由 scripts/dns_plugins.json 的 lite 字段决定。默认构建不再内置
// 任何服务商：内置服务商已迁移到 DNS 插件（helper-extra），主程序只负责
// 加载插件。确需把服务商编进主程序时，在 lite 中追加 ID 并重新生成。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const legoModule = "github.com/go-acme/lego/v4"

// defaultLite 是 scripts/dns_plugins.json 缺失时的兜底集合。
// 默认构建不内置服务商，所有服务商由 DNS 插件（helper）提供。
var defaultLite = []string{}

type pluginConfig struct {
	Lite []string `json:"lite"`
}

func main() {
	root := repoRoot()
	legoDir := moduleDir(root, legoModule)
	providersDir := filepath.Join(legoDir, "providers", "dns")

	entries, err := os.ReadDir(providersDir)
	check(err)

	liteList := loadLite(root)
	liteSet := make(map[string]bool, len(liteList))
	for _, id := range liteList {
		liteSet[id] = true
	}

	aliases := parseAliases(filepath.Join(providersDir, "zz_gen_dns_providers.go"))

	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if id == "internal" || id == "manual" {
			continue
		}
		if _, err := os.Stat(filepath.Join(providersDir, id, "dns_provider.go")); err != nil {
			// 部分服务商的构造函数在别的文件名中，用内容检查兜底。
			if !hasNewDNSProvider(filepath.Join(providersDir, id)) {
				fmt.Fprintf(os.Stderr, "skip %s: no NewDNSProvider\n", id)
				continue
			}
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for id := range liteSet {
		if !contains(ids, id) {
			fmt.Fprintf(os.Stderr, "lite 配置中的服务商不存在: %s\n", id)
			os.Exit(1)
		}
	}

	outRoot := filepath.Join(root, "internal", "dnsplugin")
	builtinRoot := filepath.Join(outRoot, "builtin")
	generated := 0
	for _, id := range ids {
		writePlugin(filepath.Join(outRoot, "providers", id, "plugin.go"), id, aliases[id])
		generated++
	}

	extraList := make([]string, 0, len(ids)-len(liteList))
	for _, id := range ids {
		if !liteSet[id] {
			extraList = append(extraList, id)
		}
	}

	// 三个互斥的构建 profile：
	//   - lite（默认）：主程序内置的常用服务商
	//   - extra（dns_extra）：其余服务商，构建成外部 helper 插件
	//   - full（dns_all）：全部服务商，直接编进主程序
	writeProfile(filepath.Join(builtinRoot, "profile_lite.go"), "!dns_all && !dns_extra", liteList)
	writeProfile(filepath.Join(builtinRoot, "profile_extra.go"), "dns_extra && !dns_all", extraList)
	writeProfile(filepath.Join(builtinRoot, "profile_full.go"), "dns_all", ids)
	fmt.Printf("generated %d provider plugins, lite=%d, extra=%d, full=%d\n", generated, len(liteList), len(extraList), len(ids))
}

func repoRoot() string {
	dir, err := os.Getwd()
	check(err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			fail("go.mod not found")
		}
		dir = parent
	}
}

func moduleDir(root, module string) string {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", module)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		fail(fmt.Sprintf("go list %s: %v: %s", module, err, out.String()))
	}
	return strings.TrimSpace(out.String())
}

func loadLite(root string) []string {
	path := filepath.Join(root, "scripts", "dns_plugins.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return append([]string(nil), defaultLite...)
		}
		check(err)
	}
	var cfg pluginConfig
	check(json.Unmarshal(data, &cfg))
	// lite 显式为空表示默认构建不内置任何服务商，不能回退到历史集合。
	sort.Strings(cfg.Lite)
	return cfg.Lite
}

// parseAliases 从 lego 自动生成的分发文件里解析 规范包名 -> 别名列表。
func parseAliases(path string) map[string][]string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析 %s 失败，将不生成别名: %v\n", path, err)
		return nil
	}
	imports := map[string]string{}
	for _, spec := range file.Imports {
		pkgPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(pkgPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = pkgPath
	}
	out := map[string][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "NewDNSChallengeProviderByName" {
			return true
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			var names []string
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if value, err := strconv.Unquote(lit.Value); err == nil {
					names = append(names, value)
				}
			}
			if len(names) == 0 {
				return true
			}
			pkg := firstPackage(clause.Body, imports)
			if pkg == "" {
				return true
			}
			id := filepath.Base(pkg)
			for _, name := range names {
				if name != id && !contains(out[id], name) {
					out[id] = append(out[id], name)
				}
			}
			return true
		})
		return true
	})
	for id := range out {
		sort.Strings(out[id])
	}
	return out
}

func firstPackage(body []ast.Stmt, imports map[string]string) string {
	for _, stmt := range body {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok || len(ret.Results) == 0 {
			continue
		}
		call, ok := ret.Results[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok {
			continue
		}
		if pkg, found := imports[ident.Name]; found {
			return pkg
		}
	}
	return ""
}

func hasNewDNSProvider(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err == nil && bytes.Contains(data, []byte("func NewDNSProvider()")) {
			return true
		}
	}
	return false
}

func writePlugin(path, id string, aliases []string) {
	var buf bytes.Buffer
	buf.WriteString("// Code generated by scripts/gen_dns_plugins; DO NOT EDIT.\n\n")
	fmt.Fprintf(&buf, "// Package %s 注册 lego %s DNS 服务商。\npackage %s\n\n", id, id, id)
	buf.WriteString("import (\n")
	buf.WriteString("\t\"github.com/go-acme/lego/v4/challenge\"\n")
	fmt.Fprintf(&buf, "\tlego \"%s/providers/dns/%s\"\n", legoModule, id)
	buf.WriteString("\t\"lactssl/internal/dnsplugin\"\n")
	buf.WriteString(")\n\n")
	buf.WriteString("type plugin struct{}\n\n")
	fmt.Fprintf(&buf, "func (plugin) ID() string { return %q }\n\n", id)
	if len(aliases) == 0 {
		buf.WriteString("func (plugin) Aliases() []string { return nil }\n\n")
	} else {
		buf.WriteString("func (plugin) Aliases() []string {\n\treturn []string{")
		for i, alias := range aliases {
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "%q", alias)
		}
		buf.WriteString("}\n}\n\n")
	}
	buf.WriteString("func (plugin) Source() dnsplugin.Source { return dnsplugin.SourceBuiltin }\n\n")
	buf.WriteString("func (plugin) New(map[string]string) (challenge.Provider, error) {\n\treturn lego.NewDNSProvider()\n}\n\n")
	buf.WriteString("func init() { dnsplugin.Register(plugin{}) }\n")
	writeGo(path, buf.Bytes())
}

func writeProfile(path, tag string, ids []string) {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	var buf bytes.Buffer
	buf.WriteString("// Code generated by scripts/gen_dns_plugins; DO NOT EDIT.\n\n")
	fmt.Fprintf(&buf, "//go:build %s\n\n", tag)
	buf.WriteString("// Package builtin 按构建标签注册内置 DNS 服务商插件。\npackage builtin\n\n")
	if len(sorted) > 0 {
		buf.WriteString("import (\n")
		for _, id := range sorted {
			fmt.Fprintf(&buf, "\t_ \"lactssl/internal/dnsplugin/providers/%s\"\n", id)
		}
		buf.WriteString(")\n")
	}
	writeGo(path, buf.Bytes())
}

func writeGo(path string, data []byte) {
	formatted, err := format.Source(data)
	if err != nil {
		fail(fmt.Sprintf("格式化 %s 失败: %v", path, err))
	}
	check(os.MkdirAll(filepath.Dir(path), 0o755))
	check(os.WriteFile(path, formatted, 0o644))
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
