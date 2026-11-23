package catalog

import "github.com/AXmishell/LACTSSL-Plugins/i18n"

// LocalizedName 返回服务商或部署目标的展示名称。
func (p Provider) LocalizedName(locale i18n.Locale) string {
	if text := localize(locale, p.LabelKey); text != "" {
		return text
	}
	if p.Label != "" {
		return p.Label
	}
	return p.Value
}

// LocalizedDesc 返回服务商或部署目标的说明。
func (p Provider) LocalizedDesc(locale i18n.Locale) string {
	if text := localize(locale, p.DescKey); text != "" {
		return text
	}
	return p.Desc
}

// LocalizedLabel 返回字段标签。
func (f Field) LocalizedLabel(locale i18n.Locale) string {
	if text := localize(locale, f.LabelKey); text != "" {
		return text
	}
	return f.Label
}

// LocalizedPlaceholder 返回字段占位提示。
func (f Field) LocalizedPlaceholder(locale i18n.Locale) string {
	if text := localize(locale, f.PlaceholderKey); text != "" {
		return text
	}
	return f.Placeholder
}

// LocalizedHelp 返回字段帮助文本。
func (f Field) LocalizedHelp(locale i18n.Locale) string {
	if text := localize(locale, f.HelpKey); text != "" {
		return text
	}
	return f.Help
}

// LocalizedOption 返回下拉选项的展示文本。
func (o Option) LocalizedLabel(locale i18n.Locale) string {
	if text := localize(locale, o.LabelKey); text != "" {
		return text
	}
	return o.Label
}

func localize(locale i18n.Locale, key string) string {
	if key == "" {
		return ""
	}
	text := i18n.T(locale, key, nil)
	if text == key {
		return ""
	}
	return text
}
