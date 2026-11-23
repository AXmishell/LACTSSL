package notifyplugin

import (
	"sort"
	"sync"
)

// builtinState 保存编译进主程序的渠道元数据。
var builtinState = struct {
	sync.RWMutex
	channels map[string]ExternalChannel
}{
	channels: make(map[string]ExternalChannel),
}

// SetBuiltinChannels 替换内置通知渠道元数据清单。
func SetBuiltinChannels(channels []ExternalChannel) {
	builtinState.Lock()
	defer builtinState.Unlock()
	builtinState.channels = make(map[string]ExternalChannel, len(channels))
	for _, channel := range channels {
		id := normalize(channel.ID)
		if id == "" {
			continue
		}
		channel.ID = id
		channel.Source = string(SourceBuiltin)
		channel.FileEnabled = true
		builtinState.channels[id] = channel
	}
}

// BuiltinChannels 返回按 ID 排序的内置通知渠道元数据。
func BuiltinChannels() []ExternalChannel {
	builtinState.RLock()
	defer builtinState.RUnlock()
	out := make([]ExternalChannel, 0, len(builtinState.channels))
	for _, channel := range builtinState.channels {
		out = append(out, channel)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// BuiltinChannelInfo 查找内置通知渠道元数据，支持别名。
func BuiltinChannelInfo(id string) (ExternalChannel, bool) {
	key := normalize(id)
	builtinState.RLock()
	defer builtinState.RUnlock()
	if channel, ok := builtinState.channels[key]; ok {
		return channel, true
	}
	for _, channel := range builtinState.channels {
		for _, alias := range channel.Aliases {
			if normalize(alias) == key {
				return channel, true
			}
		}
	}
	return ExternalChannel{}, false
}

// BuiltinChannelIDs 返回全部内置通知渠道 ID（不含别名）。
func BuiltinChannelIDs() map[string]bool {
	builtinState.RLock()
	defer builtinState.RUnlock()
	out := make(map[string]bool, len(builtinState.channels))
	for id, item := range builtinState.channels {
		out[id] = true
		for _, alias := range item.Aliases {
			if alias = normalize(alias); alias != "" {
				out[alias] = true
			}
		}
	}
	return out
}

// EnabledChannels 返回已启用的内置与外部渠道，按 ID 排序并去重。
func EnabledChannels() []ExternalChannel {
	seen := map[string]bool{}
	out := make([]ExternalChannel, 0)
	for _, channel := range BuiltinChannels() {
		if !channel.Enabled {
			continue
		}
		seen[normalize(channel.ID)] = true
		out = append(out, channel)
	}
	for _, channel := range ExternalEnabledChannels() {
		if seen[normalize(channel.ID)] {
			continue
		}
		out = append(out, channel)
	}
	return out
}

// UnregisterBuiltin 移除运行期内置实现，用于类别/条目开关重新注册。
func UnregisterBuiltin() {
	UnregisterSource(SourceBuiltin)
}
