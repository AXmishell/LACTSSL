package builtin

import (
	"strings"

	plugi18n "github.com/AXmishell/LACTSSL-Plugins/i18n"
)

func localizedProviderMap(fn func(plugi18n.Locale) string) map[string]string {
	out := map[string]string{}
	for _, locale := range plugi18n.Supported() {
		if text := strings.TrimSpace(fn(locale)); text != "" {
			out[string(locale)] = text
		}
	}
	return out
}

func localizedFieldMap(fn func(plugi18n.Locale) string) map[string]string {
	return localizedProviderMap(fn)
}

func localizedOptionMap(fn func(plugi18n.Locale) string) map[string]string {
	return localizedProviderMap(fn)
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
