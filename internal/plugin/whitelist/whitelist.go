// Package whitelist 提供 DNS / 部署 / 通知三类插件条目启用白名单的通用实现。
//
// 三类白名单的差异只在设置键、默认集合、ID 归一化与“陈旧 ID 保留”规则；
// 读写、并发串行化、JSON 编解码与旧键清理在这里统一实现，避免三份逻辑漂移。
package whitelist

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// Settings 是白名单需要的最小设置存储接口。
// 由 *store.Store 实现；测试可注入内存实现。
type Settings interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
	DeleteSetting(key string) error
}

// Spec 描述一类插件白名单的差异。
type Spec struct {
	// Kind 仅用于日志/测试标识，例如 dns、deploy、notify。
	Kind string
	// SettingKey 是保存启用 ID 数组的设置键。
	SettingKey string
	// LegacyKeys 是写入新白名单后应清理的旧设置键。
	LegacyKeys []string
	// Defaults 返回尚未保存过设置时使用的默认集合。
	Defaults func() map[string]bool
	// Normalize 统一大小写与空白；为空时使用小写去空白。
	Normalize func(string) string
	// LookupKey 把 ID 映射为 Known/Retain 的查询键；为空时使用 Normalize。
	// 部署目标需要把旧 ID 映射为规范 ID 再查询缓存，但写回时仍保留原始 ID。
	LookupKey func(string) string
	// Valid 判断 ID 是否对应一个当前已知的插件条目。
	Valid func(string) bool
	// Known 返回当前仍存在（含已停用文件缓存）的条目集合。
	Known func() map[string]bool
	// Retain 判断条目是否应无条件保留，例如内置默认项。
	Retain func(string) bool
	// AfterLoad 在读取后补充兼容项，例如旧部署 ID 提升为规范 ID。
	AfterLoad func(map[string]bool)
	// AfterDisable 在停用条目时清理关联键，例如同时删除旧部署 ID。
	AfterDisable func(map[string]bool, string)
}

// Service 是一类插件白名单的读写入口。
type Service struct {
	spec     Spec
	settings Settings
	mu       sync.Mutex
}

// New 创建白名单服务。
func New(spec Spec, settings Settings) *Service {
	if spec.Normalize == nil {
		spec.Normalize = normalizeID
	}
	return &Service{spec: spec, settings: settings}
}

// Kind 返回白名单类型。
func (s *Service) Kind() string { return s.spec.Kind }

// EnabledSet 返回当前启用集合。设置缺失或 JSON 损坏时回退到默认集合。
func (s *Service) EnabledSet() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabledSetLocked()
}

// Enabled 返回排序后的启用 ID。
func (s *Service) Enabled() []string {
	set := s.EnabledSet()
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Update 批量启用或停用条目。
// 返回实际更新的 ID 与因元数据不存在而跳过的 ID。
func (s *Service) Update(ids []string, enabled bool) (updated, skipped []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	set := s.enabledSetLocked()
	updated = make([]string, 0, len(ids))
	skipped = make([]string, 0)
	for _, raw := range ids {
		id := s.spec.Normalize(raw)
		if id == "" {
			continue
		}
		if s.spec.Valid != nil && !s.spec.Valid(id) {
			skipped = append(skipped, id)
			continue
		}
		if enabled {
			set[id] = true
		} else {
			delete(set, id)
			if s.spec.AfterDisable != nil {
				s.spec.AfterDisable(set, id)
			}
		}
		updated = append(updated, id)
	}
	if len(updated) == 0 {
		return updated, skipped, nil
	}
	return updated, skipped, s.saveLocked(set)
}

// Save 按“保留已知条目 + 默认条目”的规则写回启用集合。
func (s *Service) Save(set map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(set)
}

func (s *Service) enabledSetLocked() map[string]bool {
	raw, err := s.settings.GetSetting(s.spec.SettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return s.defaults()
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return s.defaults()
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = s.spec.Normalize(id); id != "" {
			out[id] = true
		}
	}
	if s.spec.AfterLoad != nil {
		s.spec.AfterLoad(out)
	}
	return out
}

func (s *Service) defaults() map[string]bool {
	if s.spec.Defaults == nil {
		return map[string]bool{}
	}
	src := s.spec.Defaults()
	out := make(map[string]bool, len(src))
	for id, enabled := range src {
		if id = strings.TrimSpace(id); id != "" && enabled {
			// 默认集合保留原始 ID（例如部署目标同时包含旧 ID 与规范 ID），
			// 避免归一化把旧 helper 仍依赖的 ID 合并掉。
			out[id] = true
		}
	}
	return out
}

func (s *Service) saveLocked(set map[string]bool) error {
	known := make(map[string]bool)
	if s.spec.Known != nil {
		for id := range s.spec.Known() {
			if id = s.spec.Normalize(id); id != "" {
				known[id] = true
			}
		}
	}
	ids := make([]string, 0, len(set))
	seen := make(map[string]bool, len(set))
	for id, enabled := range set {
		if !enabled {
			continue
		}
		id = strings.TrimSpace(id)
		key := s.spec.Normalize(id)
		if id == "" || key == "" || seen[key] {
			continue
		}
		lookup := key
		if s.spec.LookupKey != nil {
			lookup = s.spec.LookupKey(id)
		}
		// 清理已卸载 helper 留下的陈旧 ID，但保留内置/默认条目，
		// 以及仍属于已安装文件（含已停用文件）的条目。
		if known[lookup] || (s.spec.Retain != nil && s.spec.Retain(lookup)) {
			seen[key] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := s.settings.SetSetting(s.spec.SettingKey, string(data)); err != nil {
		return err
	}
	// 旧键不再参与判断，删除以免后续排查时产生误解。
	for _, key := range s.spec.LegacyKeys {
		if key != "" && key != s.spec.SettingKey {
			_ = s.settings.DeleteSetting(key)
		}
	}
	return nil
}

func normalizeID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
