//go:build ignore

// cataloggen 把 catalog/dns 与 catalog/deploy 下的单 provider 元数据合并为
// catalog/data.json，供 catalog 包通过 go:embed 使用。
//
// 每个 provider 一个文件，新增 provider 时只改自己的文件，避免多人同时修改
// 197 KB 的聚合 JSON 产生冲突。聚合结果按 provider ID 排序，保证输出稳定，
// 不需要再维护一个共享的顺序文件。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"lactssl/providers/catalog"
)

const dataPath = "providers/catalog/data.json"

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type dataFile struct {
	DNS                   []catalog.Provider                 `json:"dns"`
	Deploy                []catalog.Provider                 `json:"deploy"`
	DeprecatedDeployTypes map[string]string                  `json:"deprecated_deploy_types"`
	DeployMigrations      map[string]catalog.DeployMigration `json:"deploy_migrations"`
}

func main() {
	dns := readProviders("dns")
	deploy := readProviders("deploy")
	migrations := readMigrations()

	data := dataFile{
		DNS:                   sortedProviders(dns),
		Deploy:                sortedProviders(deploy),
		DeprecatedDeployTypes: migrations.DeprecatedDeployTypes,
		DeployMigrations:      migrations.DeployMigrations,
	}

	writeJSON(dataPath, data)
	fmt.Printf("catalog: %d dns, %d deploy providers\n", len(data.DNS), len(data.Deploy))
}

type migrations struct {
	DeprecatedDeployTypes map[string]string                  `json:"deprecated_deploy_types"`
	DeployMigrations      map[string]catalog.DeployMigration `json:"deploy_migrations"`
}

func readMigrations() migrations {
	raw, err := os.ReadFile("providers/catalog/migrations.json")
	check(err)
	var out migrations
	check(json.Unmarshal(raw, &out))
	if out.DeprecatedDeployTypes == nil {
		out.DeprecatedDeployTypes = map[string]string{}
	}
	if out.DeployMigrations == nil {
		out.DeployMigrations = map[string]catalog.DeployMigration{}
	}
	return out
}

func readProviders(kind string) map[string]catalog.Provider {
	dir := filepath.Join("providers/catalog", kind)
	entries, err := os.ReadDir(dir)
	check(err)
	out := make(map[string]catalog.Provider, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		check(err)
		var p catalog.Provider
		check(json.Unmarshal(raw, &p))
		if err := validateProvider(kind, path, p); err != nil {
			fail(err.Error())
		}
		if _, exists := out[p.Value]; exists {
			fail(fmt.Sprintf("%s: provider %q 重复", path, p.Value))
		}
		out[p.Value] = p
	}
	return out
}

func validateProvider(kind, path string, p catalog.Provider) error {
	if !idPattern.MatchString(p.Value) {
		return fmt.Errorf("%s: provider ID 非法: %q", path, p.Value)
	}
	if strings.TrimSpace(p.Label) == "" && strings.TrimSpace(p.LabelKey) == "" {
		return fmt.Errorf("%s: provider %q 缺少 label/label_key", path, p.Value)
	}
	for _, field := range append(append([]catalog.Field{}, p.Fields...), p.DeployFields...) {
		if strings.TrimSpace(field.Key) == "" {
			return fmt.Errorf("%s: provider %q 有空字段 key", path, p.Value)
		}
		if strings.TrimSpace(field.Label) == "" && strings.TrimSpace(field.LabelKey) == "" {
			return fmt.Errorf("%s: provider %q 字段 %q 缺少 label/label_key", path, p.Value, field.Key)
		}
	}
	return nil
}

func sortedProviders(providers map[string]catalog.Provider) []catalog.Provider {
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]catalog.Provider, 0, len(ids))
	for _, id := range ids {
		out = append(out, providers[id])
	}
	return out
}

func writeJSON(path string, value any) {
	raw, err := json.MarshalIndent(value, "", "  ")
	check(err)
	raw = append(raw, '\n')
	check(os.WriteFile(path, raw, 0o644))
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "cataloggen: "+message)
	os.Exit(1)
}
