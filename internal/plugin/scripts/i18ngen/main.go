//go:build ignore

// i18ngen 把 i18n/core 与 i18n/providers/<kind>/<id> 下的单 provider 文案
// 合并为 i18n/locales/<locale>.json，供 i18n 包通过 go:embed 使用。
//
// 新增 provider 时只改自己的 i18n 文件，避免多人同时修改 70 KB 的语言文件。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	coreDir = "../../plugins/i18n/core"
	outDir  = "../../plugins/i18n/locales"
)

func main() {
	locales := readLocales()
	catalogs := map[string]map[string]string{}
	for _, locale := range locales {
		catalogs[locale] = map[string]string{}
	}

	mergeDir(coreDir, locales, catalogs)
	mergeProviders(locales, catalogs)

	for _, locale := range locales {
		path := filepath.Join(outDir, locale+".json")
		writeJSON(path, catalogs[locale])
		fmt.Printf("i18n: %s -> %d keys\n", path, len(catalogs[locale]))
	}
}

func readLocales() []string {
	seen := map[string]bool{}
	var collect func(string, bool)
	collect = func(dir string, recurse bool) {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			return
		}
		check(err)
		for _, entry := range entries {
			if entry.IsDir() {
				if recurse {
					collect(filepath.Join(dir, entry.Name()), true)
				}
				continue
			}
			if strings.HasSuffix(entry.Name(), ".json") {
				seen[strings.TrimSuffix(entry.Name(), ".json")] = true
			}
		}
	}
	collect(coreDir, false)
	collect("../../plugins/i18n/providers", true)

	out := make([]string, 0, len(seen))
	for locale := range seen {
		out = append(out, locale)
	}
	sort.Strings(out)
	if len(out) == 0 {
		fail("plugins/i18n/core 下没有语言文件")
	}
	return out
}

func mergeDir(dir string, locales []string, catalogs map[string]map[string]string) {
	for _, locale := range locales {
		path := filepath.Join(dir, locale+".json")
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		check(err)
		merge(locale, path, raw, catalogs)
	}
}

func mergeProviders(locales []string, catalogs map[string]map[string]string) {
	for _, kind := range []string{"dns", "deploy"} {
		root := filepath.Join("../../plugins/i18n/providers", kind)
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		check(err)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			id := entry.Name()
			if _, err := os.Stat(filepath.Join("providers/catalog", kind, id+".json")); err != nil {
				fail(fmt.Sprintf("plugins/i18n/providers/%s/%s 没有对应的 providers/catalog/%s/%s.json", kind, id, kind, id))
			}
			mergeDir(filepath.Join(root, id), locales, catalogs)
		}
	}
}

func merge(locale, path string, raw []byte, catalogs map[string]map[string]string) {
	var values map[string]string
	if err := json.Unmarshal(raw, &values); err != nil {
		fail(fmt.Sprintf("%s: %v", path, err))
	}
	for key, value := range values {
		if strings.TrimSpace(key) == "" {
			fail(fmt.Sprintf("%s: 空 key", path))
		}
		if _, exists := catalogs[locale][key]; exists {
			fail(fmt.Sprintf("%s: key %q 重复", path, key))
		}
		catalogs[locale][key] = value
	}
}

func writeJSON(path string, values map[string]string) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(values))
	for _, key := range keys {
		ordered[key] = values[key]
	}
	raw, err := json.MarshalIndent(ordered, "", "  ")
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
	fmt.Fprintln(os.Stderr, "i18ngen: "+message)
	os.Exit(1)
}
