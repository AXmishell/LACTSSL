// Package registry 提供插件 ID / 别名 / 来源的通用注册表。
//
// DNS、部署、通知三类插件共享同一套冲突检测、原子替换与来源筛选逻辑；
// 各宿主包只保留类型特化的薄封装，避免三份实现随时间产生行为差异。
package registry

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// Source 标识插件来源。
type Source string

const (
	// Builtin 表示编译进主程序的内置实现。
	Builtin Source = "builtin"
	// Exec 表示外部 exec helper 插件。
	Exec Source = "exec"
)

// ErrInvalidID 表示插件 ID 或别名格式非法。
var ErrInvalidID = errors.New("plugin id is empty or invalid")

// DuplicateError 表示插件 ID 或别名冲突。
type DuplicateError struct{ ID string }

func (e *DuplicateError) Error() string { return "plugin already registered: " + e.ID }

// Adapter 描述如何从插件值中读取注册字段。
// ValidateID 为空时只拒绝空 ID；部署与通知插件可传入格式校验。
type Adapter[T any] struct {
	ID         func(T) string
	Aliases    func(T) []string
	Source     func(T) Source
	ValidateID func(string) bool
}

// Registry 是并发安全的插件注册表。
type Registry[T any] struct {
	mu      sync.RWMutex
	adapter Adapter[T]
	byID    map[string]T
	alias   map[string]string
}

// New 创建注册表。
func New[T any](adapter Adapter[T]) *Registry[T] {
	return &Registry[T]{
		adapter: adapter,
		byID:    make(map[string]T),
		alias:   make(map[string]string),
	}
}

// Register 注册一个插件并返回冲突等错误。
func (r *Registry[T]) Register(p T) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registerInto(r.byID, r.alias, p)
}

// ReplaceBuiltin 原子替换全部内置插件。
//
// 新集合全部注册成功后才交换注册表；任一条目冲突、ID 非法或来源不匹配时
// 返回错误并保留原有内置集合，外部插件不受影响。
func (r *Registry[T]) ReplaceBuiltin(plugins []T) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.replaceSourceLocked(Builtin, plugins)
}

// ReplaceSource 原子替换指定来源的全部插件。
func (r *Registry[T]) ReplaceSource(source Source, plugins []T) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.replaceSourceLocked(source, plugins)
}

// ReplaceSourceBestEffort 清空指定来源后逐个注册，返回成功项与错误。
// 用于外部插件重扫：单个坏条目不应阻止其他条目注册。
func (r *Registry[T]) ReplaceSourceBestEffort(source Source, plugins []T) ([]T, []error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearLocked(source)
	loaded := make([]T, 0, len(plugins))
	var errs []error
	for _, p := range plugins {
		if err := r.registerInto(r.byID, r.alias, p); err != nil {
			errs = append(errs, err)
			continue
		}
		loaded = append(loaded, p)
	}
	return loaded, errs
}

func (r *Registry[T]) replaceSourceLocked(source Source, plugins []T) error {
	byID := make(map[string]T, len(r.byID)+len(plugins))
	for id, p := range r.byID {
		if r.adapter.Source(p) != source {
			byID[id] = p
		}
	}
	alias := make(map[string]string, len(byID))
	for id, p := range byID {
		for _, item := range r.adapter.Aliases(p) {
			if item = normalize(item); item != "" && item != id {
				alias[item] = id
			}
		}
	}
	for _, p := range plugins {
		if r.adapter.Source(p) != source {
			return errors.New("plugin source does not match replacement target")
		}
		if err := r.registerInto(byID, alias, p); err != nil {
			return err
		}
	}
	r.byID = byID
	r.alias = alias
	return nil
}

func (r *Registry[T]) registerInto(byID map[string]T, alias map[string]string, p T) error {
	id := normalize(r.adapter.ID(p))
	if id == "" || (r.adapter.ValidateID != nil && !r.adapter.ValidateID(id)) {
		return ErrInvalidID
	}
	if _, exists := byID[id]; exists {
		return &DuplicateError{ID: id}
	}
	// 新 ID 不能占用其他插件已经注册的别名，否则 Lookup 会优先命中
	// byID，导致旧插件的凭据被新插件接收。
	if _, exists := alias[id]; exists {
		return &DuplicateError{ID: id}
	}
	for _, item := range r.adapter.Aliases(p) {
		item = normalize(item)
		if item == "" {
			continue
		}
		if r.adapter.ValidateID != nil && !r.adapter.ValidateID(item) {
			return ErrInvalidID
		}
		if _, exists := byID[item]; exists {
			return &DuplicateError{ID: item}
		}
		if _, exists := alias[item]; exists {
			return &DuplicateError{ID: item}
		}
	}
	byID[id] = p
	for _, item := range r.adapter.Aliases(p) {
		item = normalize(item)
		if item != "" && item != id {
			alias[item] = id
		}
	}
	return nil
}

// Lookup 按 ID 或别名查找插件。
func (r *Registry[T]) Lookup(name string) (T, bool) {
	var zero T
	key := normalize(name)
	if key == "" {
		return zero, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p, ok := r.byID[key]; ok {
		return p, true
	}
	if id, ok := r.alias[key]; ok {
		p, found := r.byID[id]
		return p, found
	}
	return zero, false
}

// Has 判断插件是否已注册（含别名）。
func (r *Registry[T]) Has(name string) bool {
	_, ok := r.Lookup(name)
	return ok
}

// List 返回按规范化 ID 排序的全部插件。
func (r *Registry[T]) List() []T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]T, 0, len(r.byID))
	for _, p := range r.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return normalize(r.adapter.ID(out[i])) < normalize(r.adapter.ID(out[j]))
	})
	return out
}

// ListBySource 返回指定来源的插件。
func (r *Registry[T]) ListBySource(source Source) []T {
	all := r.List()
	out := all[:0]
	for _, p := range all {
		if r.adapter.Source(p) == source {
			out = append(out, p)
		}
	}
	return out
}

// UnregisterSource 移除指定来源的全部插件，用于外部插件重扫。
func (r *Registry[T]) UnregisterSource(source Source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clearLocked(source)
}

func (r *Registry[T]) clearLocked(source Source) {
	for id, p := range r.byID {
		if r.adapter.Source(p) != source {
			continue
		}
		delete(r.byID, id)
		for _, alias := range r.adapter.Aliases(p) {
			alias = normalize(alias)
			if current, ok := r.alias[alias]; ok && current == id {
				delete(r.alias, alias)
			}
		}
	}
}

// Reset 清空注册表，仅供测试使用。
func (r *Registry[T]) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID = make(map[string]T)
	r.alias = make(map[string]string)
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
