package meta

import "lactssl/providers/catalog"

// legacyHints 为 lego v5 已移除的 provider 补充说明。
var legacyHints = map[string]string{
	"azure":         "lego v5 已移除 azure provider，请改用 azuredns。",
	"brandit":       "lego v5 已移除 brandit provider，请改用其他服务商或 lego v4 插件包。",
	"cloudxns":      "lego v5 已移除 cloudxns provider，请改用其他服务商或 lego v4 插件包。",
	"dnspod":        "lego v5 已移除 dnspod provider，请改用 tencentcloud 或 lego v4 插件包。",
	"googledomains": "lego v5 已移除 googledomains provider，请改用其他服务商或 lego v4 插件包。",
	"iij":           "lego v5 已移除 iij provider，请改用 iijdpf 或 lego v4 插件包。",
	"iwantmyname":   "lego v5 已移除 iwantmyname provider，请改用其他服务商或 lego v4 插件包。",
}

func withLegacyHint(def catalog.Provider) catalog.Provider {
	if hint, ok := legacyHints[normalize(def.Value)]; ok {
		def.Desc = hint
		def.DescKey = ""
	}
	return def
}
