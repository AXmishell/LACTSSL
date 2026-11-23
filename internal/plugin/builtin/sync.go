package builtin

import "strings"

// IDSet 把配置中的 ID 列表转换为小写集合。
func IDSet(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			out[id] = true
		}
	}
	return out
}
