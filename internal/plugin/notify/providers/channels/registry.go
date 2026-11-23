package channels

import (
	"fmt"
	"sync"

	notify "lactssl/internal/plugin/notify"
)

// registry 由各 channel_*.go 的 init 填充。
// 新增渠道只需增加一个文件，不再修改共享的渠道列表。
var registry = struct {
	sync.RWMutex
	items []channel
}{}

func registerChannel(item channel) {
	registry.Lock()
	defer registry.Unlock()
	id := item.Definition().ID
	for _, existing := range registry.items {
		if existing.Definition().ID == id {
			panic(fmt.Sprintf("notify channel already registered: %s", id))
		}
	}
	registry.items = append(registry.items, item)
}

func registeredChannels() []channel {
	registry.RLock()
	defer registry.RUnlock()
	out := make([]channel, len(registry.items))
	copy(out, registry.items)
	return out
}

// RegisterAll 注册全部通知渠道，供 bundle helper 使用。
func RegisterAll() {
	for _, item := range registeredChannels() {
		notify.RegisterNative(item)
	}
}

// RegisterByID 只注册指定渠道，供单插件构建使用。
// 返回 false 表示渠道不存在。
func RegisterByID(id string) bool {
	for _, item := range registeredChannels() {
		if item.Definition().ID == id {
			notify.RegisterNative(item)
			return true
		}
	}
	return false
}

// IDs 返回全部渠道 ID，按注册顺序。
func IDs() []string {
	items := registeredChannels()
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Definition().ID)
	}
	return out
}
