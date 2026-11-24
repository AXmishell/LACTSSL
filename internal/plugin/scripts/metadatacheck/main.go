//go:build ignore

// metadatacheck 校验 provider 元数据、i18n、图标与插件清单是否一致。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	notifyplugin "lactssl/internal/plugin/host/notify"
	"lactssl/providers/catalog"
	dnsmeta "lactssl/providers/dns/lego5/meta"
)

type manifest struct {
	Plugins []struct {
		Kind      string   `json:"kind"`
		ID        string   `json:"id"`
		Providers []string `json:"providers"`
	} `json:"plugins"`
}

func main() {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	raw, err := os.ReadFile("plugins.json")
	if err != nil {
		fail(err.Error())
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		fail(err.Error())
	}

	dnsIDs := stringSet(dnsmeta.IDs())
	notifyIDs := stringSet(notifyplugin.OfficialChannelIDs())
	deployCatalog := stringSet(providerValues(catalog.Deployers()))
	for _, entry := range m.Plugins {
		switch entry.Kind {
		case "dns":
			got := stringSet(entry.Providers)
			for id := range dnsIDs {
				if !got[id] {
					add("plugins.json DNS 清单缺少 %s", id)
				}
			}
			for id := range got {
				if !dnsIDs[id] {
					add("plugins.json DNS 清单包含未知 ID %s", id)
				}
			}
		case "notify":
			got := stringSet(entry.Providers)
			for id := range notifyIDs {
				if !got[id] {
					add("plugins.json 通知清单缺少 %s", id)
				}
			}
			for id := range got {
				if !notifyIDs[id] {
					add("plugins.json 通知清单包含未知 ID %s", id)
				}
			}
		case "deploy":
			for _, id := range entry.Providers {
				if !deployCatalog[id] {
					add("plugins.json 部署清单包含 catalog 未定义的 ID %s", id)
				}
			}
		}
	}

	for _, kind := range []string{"dns", "deploy"} {
		catalogIDs := catalogIDsFor(kind)
		i18nDir := filepath.Join("..", "..", "plugins", "i18n", "providers", kind)
		i18nIDs := dirSet(i18nDir)
		for id := range catalogIDs {
			if !i18nIDs[id] {
				add("%s/%s 缺少 i18n 目录", kind, id)
			}
		}
		for id := range i18nIDs {
			if !catalogIDs[id] {
				add("%s/%s 存在 i18n 但没有 catalog 条目", kind, id)
			}
		}
		iconDir := filepath.Join("assets", "icons", kind)
		for id := range iconSet(iconDir) {
			if !catalogIDs[id] {
				add("%s/%s 存在图标但没有 catalog 条目", kind, id)
			}
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		for _, msg := range errs {
			fmt.Fprintln(os.Stderr, msg)
		}
		os.Exit(1)
	}
	fmt.Println("metadata check: ok")
}

func providerValues(items []catalog.Provider) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, strings.ToLower(strings.TrimSpace(item.Value)))
	}
	return out
}

func catalogIDsFor(kind string) map[string]bool {
	values := make([]string, 0, 256)
	dir := filepath.Join("providers/catalog", kind)
	entries, err := os.ReadDir(dir)
	if err != nil {
		fail(err.Error())
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		values = append(values, strings.TrimSuffix(entry.Name(), ".json"))
	}
	return stringSet(values)
}

func dirSet(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		fail(err.Error())
	}
	out := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			out[strings.ToLower(entry.Name())] = true
		}
	}
	return out
}

func iconSet(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}
		}
		fail(err.Error())
	}
	out := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if idx := strings.LastIndexByte(name, '.'); idx > 0 {
			name = name[:idx]
		}
		out[strings.ToLower(name)] = true
	}
	return out
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			out[value] = true
		}
	}
	return out
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
