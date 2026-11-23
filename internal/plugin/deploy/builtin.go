package deployplugin

import (
	"sort"
	"sync"
)

// builtinState 保存编译进主程序的部署目标元数据。
// 未启用条目仍需用于插件页展示、历史账号脱敏和开关切换。
var builtinState = struct {
	sync.RWMutex
	deployers map[string]ExternalDeployer
}{
	deployers: make(map[string]ExternalDeployer),
}

// SetBuiltinDeployers 替换内置部署目标元数据清单。
func SetBuiltinDeployers(deployers []ExternalDeployer) {
	builtinState.Lock()
	defer builtinState.Unlock()
	builtinState.deployers = make(map[string]ExternalDeployer, len(deployers))
	for _, deployer := range deployers {
		id := normalize(deployer.ID)
		if id == "" {
			continue
		}
		deployer.ID = id
		deployer.Source = string(SourceBuiltin)
		deployer.FileEnabled = true
		builtinState.deployers[id] = deployer
	}
}

// BuiltinDeployers 返回按 ID 排序的内置部署目标元数据。
func BuiltinDeployers() []ExternalDeployer {
	builtinState.RLock()
	defer builtinState.RUnlock()
	out := make([]ExternalDeployer, 0, len(builtinState.deployers))
	for _, deployer := range builtinState.deployers {
		out = append(out, deployer)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// BuiltinDeployerInfo 查找内置部署目标元数据，支持别名。
func BuiltinDeployerInfo(id string) (ExternalDeployer, bool) {
	key := normalize(id)
	builtinState.RLock()
	defer builtinState.RUnlock()
	if deployer, ok := builtinState.deployers[key]; ok {
		return deployer, true
	}
	for _, deployer := range builtinState.deployers {
		for _, alias := range deployer.Aliases {
			if normalize(alias) == key {
				return deployer, true
			}
		}
	}
	return ExternalDeployer{}, false
}

// BuiltinDeployerIDs 返回全部内置部署目标 ID（不含别名）。
func BuiltinDeployerIDs() map[string]bool {
	builtinState.RLock()
	defer builtinState.RUnlock()
	out := make(map[string]bool, len(builtinState.deployers))
	for id, item := range builtinState.deployers {
		out[id] = true
		for _, alias := range item.Aliases {
			if alias = normalize(alias); alias != "" {
				out[alias] = true
			}
		}
	}
	return out
}

// UnregisterBuiltin 移除运行期内置实现，用于类别/条目开关重新注册。
func UnregisterBuiltin() {
	UnregisterSource(SourceBuiltin)
}
