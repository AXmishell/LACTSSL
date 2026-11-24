// Package state 在 present/cleanup 两次一次性进程调用之间传递 lego provider 的临时记录 ID。
//
// lego 的许多 DNS provider 在 Present 时把 token -> recordID 保存在
// provider 实例的未导出 recordIDs map 中，CleanUp 再按 token 取回。协议
// 每次动作都会启动新进程，直接调用会导致 CleanUp 报 "unknown record ID"，
// TXT 记录无法清理。这个包在 Present 成功后把 map 持久化到用户私有目录，
// CleanUp 前再恢复；没有 recordIDs 字段的 native provider 会原样透传。
//
// 这是对 lego 私有实现的兼容层，不应成为新 provider 的设计依赖。长期方案
// 是让宿主在同一个进程内完成 present/cleanup，或改用按 name/value 删除的
// native 实现。
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"unsafe"

	dns "lactssl/internal/plugin/host/dns"
)

const stateDirEnv = "LACTSSL_PLUGIN_STATE_DIR"

var fileMu sync.Mutex

type wrapper struct {
	id       string
	provider dns.NativeChallengeProvider
}

// Wrap 给 provider 增加跨进程的 recordIDs 持久化。
func Wrap(id string, provider dns.NativeChallengeProvider) dns.NativeChallengeProvider {
	if provider == nil {
		return nil
	}
	return wrapper{id: strings.TrimSpace(id), provider: provider}
}

func (w wrapper) Present(domain, token, keyAuth string) error {
	if err := w.provider.Present(domain, token, keyAuth); err != nil {
		return err
	}
	if err := save(w.provider, w.id, domain, token); err != nil {
		fmt.Fprintf(os.Stderr, "dns state: save %s: %v\n", w.id, err)
	}
	return nil
}

func (w wrapper) CleanUp(domain, token, keyAuth string) error {
	if err := load(w.provider, w.id, domain, token); err != nil {
		fmt.Fprintf(os.Stderr, "dns state: load %s: %v\n", w.id, err)
	}
	err := w.provider.CleanUp(domain, token, keyAuth)
	if err == nil {
		_ = remove(w.id, domain, token)
	}
	return err
}

type fileState struct {
	Provider string                     `json:"provider"`
	Domain   string                     `json:"domain"`
	Values   map[string]json.RawMessage `json:"record_ids"`
}

func statePath(id, domain, token string) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(id + "\x00" + strings.ToLower(strings.TrimSpace(domain)) + "\x00" + token))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".json"), nil
}

func stateDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(stateDirEnv)); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		_ = os.Chmod(dir, 0o700)
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "lactssl", "dns-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	return dir, nil
}

func save(provider any, id, domain, token string) error {
	field, ok := recordIDsField(provider)
	if !ok {
		return nil
	}
	values := make(map[string]json.RawMessage, field.Len())
	for _, key := range field.MapKeys() {
		name := key.String()
		raw, err := encodeValue(field.MapIndex(key))
		if err != nil {
			return err
		}
		values[name] = raw
	}
	if len(values) == 0 {
		return nil
	}
	path, err := statePath(id, domain, token)
	if err != nil {
		return err
	}
	data, err := json.Marshal(fileState{Provider: id, Domain: domain, Values: values})
	if err != nil {
		return err
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func load(provider any, id, domain, token string) error {
	field, ok := recordIDsField(provider)
	if !ok {
		return nil
	}
	path, err := statePath(id, domain, token)
	if err != nil {
		return err
	}
	fileMu.Lock()
	raw, err := os.ReadFile(path)
	fileMu.Unlock()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var saved fileState
	if err := json.Unmarshal(raw, &saved); err != nil {
		return err
	}
	if saved.Provider != id || !strings.EqualFold(strings.TrimSpace(saved.Domain), strings.TrimSpace(domain)) {
		return fmt.Errorf("state does not match provider/domain")
	}
	merged := reflect.MakeMap(field.Type())
	for _, key := range field.MapKeys() {
		merged.SetMapIndex(key, field.MapIndex(key))
	}
	for name, value := range saved.Values {
		decoded, err := decodeValue(field.Type().Elem(), value)
		if err != nil {
			return fmt.Errorf("decode %s: %w", name, err)
		}
		merged.SetMapIndex(reflect.ValueOf(name).Convert(field.Type().Key()), decoded)
	}
	// field 是未导出字段；只能通过 unsafe 写回 map。
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(merged)
	return nil
}

func remove(id, domain, token string) error {
	path, err := statePath(id, domain, token)
	if err != nil {
		return err
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func recordIDsField(provider any) (reflect.Value, bool) {
	if unwrapper, ok := provider.(interface{ Unwrap() any }); ok {
		provider = unwrapper.Unwrap()
	}
	value := reflect.ValueOf(provider)
	for value.IsValid() && (value.Kind() == reflect.Ptr || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	field := value.FieldByName("recordIDs")
	if !field.IsValid() || field.Kind() != reflect.Map || field.Type().Key().Kind() != reflect.String {
		return reflect.Value{}, false
	}
	if !field.CanAddr() {
		return reflect.Value{}, false
	}
	return field, true
}

func encodeValue(value reflect.Value) (json.RawMessage, error) {
	value = deref(value)
	if !value.IsValid() {
		return json.Marshal(nil)
	}
	switch value.Kind() {
	case reflect.String:
		return json.Marshal(value.String())
	case reflect.Bool:
		return json.Marshal(value.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return json.Marshal(value.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return json.Marshal(value.Uint())
	case reflect.Struct:
		out := make(map[string]any, value.NumField())
		for i := 0; i < value.NumField(); i++ {
			raw, err := encodeValue(value.Field(i))
			if err != nil {
				return nil, err
			}
			var decoded any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil, err
			}
			out[value.Type().Field(i).Name] = decoded
		}
		return json.Marshal(out)
	default:
		return nil, fmt.Errorf("unsupported record ID type %s", value.Type())
	}
}

func decodeValue(target reflect.Type, raw json.RawMessage) (reflect.Value, error) {
	if target.Kind() == reflect.Ptr {
		if string(raw) == "null" {
			return reflect.Zero(target), nil
		}
		inner, err := decodeValue(target.Elem(), raw)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(target.Elem())
		out.Elem().Set(inner)
		return out, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return reflect.Value{}, err
	}
	switch target.Kind() {
	case reflect.String:
		text, ok := decoded.(string)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected string")
		}
		return reflect.ValueOf(text).Convert(target), nil
	case reflect.Bool:
		flag, ok := decoded.(bool)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected bool")
		}
		return reflect.ValueOf(flag).Convert(target), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number, ok := decoded.(float64)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected number")
		}
		return reflect.ValueOf(int64(number)).Convert(target), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number, ok := decoded.(float64)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected number")
		}
		return reflect.ValueOf(uint64(number)).Convert(target), nil
	case reflect.Struct:
		object, ok := decoded.(map[string]any)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected object")
		}
		out := reflect.New(target).Elem()
		for i := 0; i < target.NumField(); i++ {
			value, exists := object[target.Field(i).Name]
			if !exists {
				continue
			}
			rawValue, err := json.Marshal(value)
			if err != nil {
				return reflect.Value{}, err
			}
			fieldValue, err := decodeValue(target.Field(i).Type, rawValue)
			if err != nil {
				return reflect.Value{}, err
			}
			// 结构体字段可能是未导出的（shellrent 的 reqKey）。
			reflect.NewAt(fieldValue.Type(), unsafe.Pointer(out.Field(i).UnsafeAddr())).Elem().Set(fieldValue)
		}
		return out, nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported record ID type %s", target)
	}
}

func deref(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Ptr || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}
