package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// ConfigBackupDirectory 是配置文件快照目录（相对 data_dir）。
	ConfigBackupDirectory = "config-backups"
	// ConfigBackupAutoRetention 是自动快照保留数量。
	ConfigBackupAutoRetention = 20
	// ConfigBackupManualRetention 是手动快照保留数量。
	ConfigBackupManualRetention = 50
	// configBackupMaxSize 限制单个快照大小，避免异常文件占满磁盘。
	configBackupMaxSize      = 1 << 20
	configBackupReasonManual = "manual"
)

// ErrConfigBackupNotFound 表示配置文件快照不存在。
var ErrConfigBackupNotFound = errors.New("config backup not found")

var configBackupIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}$`)

// configBackupMeta 是快照元数据，不包含任何密钥明文。
type ConfigBackupMeta struct {
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	Reason          string    `json:"reason"`
	Note            string    `json:"note,omitempty"`
	Size            int64     `json:"size"`
	SHA256          string    `json:"sha256"`
	SaltFingerprint string    `json:"salt_fingerprint,omitempty"`
	JWTFingerprint  string    `json:"jwt_fingerprint,omitempty"`
	Actor           string    `json:"actor,omitempty"`
	AppVersion      string    `json:"app_version,omitempty"`
}

// ConfigBackupInfo 是返回给前端的快照信息。
type ConfigBackupInfo struct {
	ConfigBackupMeta
	SaltMatches bool `json:"salt_matches"`
}

// ConfigBackupDir 返回快照目录。
func ConfigBackupDir(dataDir string) string {
	return filepath.Join(dataDir, ConfigBackupDirectory)
}

// CreateConfigBackup 从当前配置文件创建快照。
func CreateConfigBackup(cfgPath, dataDir, reason, note, actor, appVersion string) (*ConfigBackupInfo, error) {
	if stat, err := os.Stat(cfgPath); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	} else if stat.Size() > configBackupMaxSize {
		return nil, fmt.Errorf("配置文件超过 %d 字节，拒绝备份", configBackupMaxSize)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	return CreateConfigBackupFromData(cfgPath, dataDir, data, reason, note, actor, appVersion)
}

// CreateConfigBackupFromData 用已经读取的配置内容创建快照。
// 调用方应保证 data 与当前配置文件一致（例如在同一把写锁内读取）。
func CreateConfigBackupFromData(cfgPath, dataDir string, data []byte, reason, note, actor, appVersion string) (*ConfigBackupInfo, error) {
	if len(data) == 0 {
		return nil, errors.New("配置文件为空，无法备份")
	}
	if len(data) > configBackupMaxSize {
		return nil, fmt.Errorf("配置文件超过 %d 字节，拒绝备份", configBackupMaxSize)
	}
	if strings.TrimSpace(reason) == "" {
		reason = "before-save"
	}
	dir := ConfigBackupDir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建配置备份目录失败: %w", err)
	}
	_ = os.Chmod(dir, 0o700)

	id, err := newConfigBackupID(dir)
	if err != nil {
		return nil, err
	}
	meta := ConfigBackupMeta{
		ID:         id,
		CreatedAt:  time.Now().UTC(),
		Reason:     reason,
		Note:       strings.TrimSpace(note),
		Size:       int64(len(data)),
		SHA256:     configRevision(data),
		Actor:      strings.TrimSpace(actor),
		AppVersion: strings.TrimSpace(appVersion),
	}
	meta.SaltFingerprint, meta.JWTFingerprint = configSecretFingerprints(data)

	dataPath := filepath.Join(dir, id+".yaml")
	metaPath := filepath.Join(dir, id+".json")
	if err := writeFileAtomic(dataPath, data); err != nil {
		return nil, fmt.Errorf("写入配置备份失败: %w", err)
	}
	rawMeta, err := json.MarshalIndent(meta, "", "  ")
	if err == nil {
		err = writeFileAtomic(metaPath, rawMeta)
	}
	if err != nil {
		_ = os.Remove(dataPath)
		return nil, fmt.Errorf("写入配置备份元数据失败: %w", err)
	}

	pruneConfigBackups(dataDir)
	return &ConfigBackupInfo{ConfigBackupMeta: meta, SaltMatches: true}, nil
}

// ListConfigBackups 返回快照列表，并按当前配置的 salt 标记是否匹配。
func ListConfigBackups(cfgPath, dataDir string) ([]ConfigBackupInfo, error) {
	metas, err := readConfigBackupMetas(dataDir)
	if err != nil {
		return nil, err
	}
	currentSalt := ""
	if data, err := os.ReadFile(cfgPath); err == nil {
		currentSalt, _ = configSecretFingerprints(data)
	}
	out := make([]ConfigBackupInfo, 0, len(metas))
	for _, meta := range metas {
		if _, err := os.Stat(filepath.Join(ConfigBackupDir(dataDir), meta.ID+".yaml")); err != nil {
			continue
		}
		matches := meta.SaltFingerprint == currentSalt
		out = append(out, ConfigBackupInfo{ConfigBackupMeta: meta, SaltMatches: matches})
	}
	return out, nil
}

// LoadConfigBackup 读取一份快照的内容。
func LoadConfigBackup(cfgPath, dataDir, id string) (*ConfigBackupInfo, []byte, error) {
	if !validConfigBackupID(id) {
		return nil, nil, ErrConfigBackupNotFound
	}
	dir := ConfigBackupDir(dataDir)
	rawMeta, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil, nil, ErrConfigBackupNotFound
	}
	var meta ConfigBackupMeta
	if err := json.Unmarshal(rawMeta, &meta); err != nil || meta.ID != id {
		return nil, nil, ErrConfigBackupNotFound
	}
	dataPath := filepath.Join(dir, id+".yaml")
	if stat, statErr := os.Stat(dataPath); statErr != nil || stat.Size() > configBackupMaxSize {
		return nil, nil, ErrConfigBackupNotFound
	}
	data, err := os.ReadFile(dataPath)
	if err != nil {
		return nil, nil, ErrConfigBackupNotFound
	}
	currentSalt := ""
	if current, readErr := os.ReadFile(cfgPath); readErr == nil {
		currentSalt, _ = configSecretFingerprints(current)
	}
	return &ConfigBackupInfo{
		ConfigBackupMeta: meta,
		SaltMatches:      meta.SaltFingerprint == currentSalt,
	}, data, nil
}

// DeleteConfigBackup 删除一份快照。
func DeleteConfigBackup(dataDir, id string) error {
	if !validConfigBackupID(id) {
		return ErrConfigBackupNotFound
	}
	dir := ConfigBackupDir(dataDir)
	removed := false
	for _, ext := range []string{".yaml", ".json"} {
		err := os.Remove(filepath.Join(dir, id+ext))
		if err == nil {
			removed = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除配置备份失败: %w", err)
		}
	}
	if !removed {
		return ErrConfigBackupNotFound
	}
	return nil
}

// RestoreConfigBackup 用快照中的白名单字段覆盖当前配置。
// 密钥、data_dir、database.path 等不在白名单内的字段保持不变。
func RestoreConfigBackup(cfgPath, dataDir, id, actor, appVersion string) (*EditableUpdateResult, error) {
	_, data, err := LoadConfigBackup(cfgPath, dataDir, id)
	if err != nil {
		return nil, err
	}
	fc := defaults()
	if err := yaml.Unmarshal(data, fc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSetupConfigInvalid, err)
	}
	revision, err := ConfigRevision(cfgPath)
	if err != nil {
		return nil, err
	}
	return UpdateEditableConfig(
		cfgPath,
		dataDir,
		SetupConfigPatchFrom(setupConfigFromFile(fc)),
		revision,
		"before-restore",
		"restore "+id,
		actor,
		appVersion,
	)
}

// UpdateEditableConfig 在写锁内完成“校验 + 快照 + 原子写”。
// 调用方必须保证同一配置文件不会被并发写入。
func UpdateEditableConfig(cfgPath, dataDir string, patch SetupConfigPatch, expectedRevision, reason, note, actor, appVersion string) (*EditableUpdateResult, error) {
	prepared, err := PrepareConfigUpdate(cfgPath, patch, expectedRevision)
	if err != nil {
		return nil, err
	}
	if len(prepared.ChangedFields) == 0 {
		return &EditableUpdateResult{
			Revision: configRevision(prepared.Original),
			Noop:     true,
		}, nil
	}
	if err := preflightListenChange(cfgPath, prepared.EffectiveAddr); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSetupConfigInvalid, err)
	}
	backup, err := CreateConfigBackupFromData(cfgPath, dataDir, prepared.Original, reason, note, actor, appVersion)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfigBackupFailed, err)
	}
	if err := writeFileAtomic(cfgPath, prepared.Next); err != nil {
		return nil, err
	}
	return &EditableUpdateResult{
		Revision:      configRevision(prepared.Next),
		ChangedFields: prepared.ChangedFields,
		Backup:        backup,
	}, nil
}

// preflightListenChange 在端口发生变化时尝试绑定新地址，提前发现权限或占用问题。
// 仅修改 host、端口不变时跳过：旧进程仍占用该端口，预检会误报。
func preflightListenChange(cfgPath, nextAddr string) error {
	nextAddr = strings.TrimSpace(nextAddr)
	if nextAddr == "" {
		return nil
	}
	currentAddr, err := EffectiveAddr(cfgPath)
	if err != nil {
		return nil
	}
	_, currentPort, currentErr := net.SplitHostPort(strings.TrimSpace(currentAddr))
	_, nextPort, nextErr := net.SplitHostPort(nextAddr)
	if currentErr != nil || nextErr != nil || currentPort == nextPort {
		return nil
	}
	listener, err := net.Listen("tcp", nextAddr)
	if err != nil {
		return fmt.Errorf("新的监听地址不可用（端口被占用或无权限）: %s: %w", nextAddr, err)
	}
	_ = listener.Close()
	return nil
}

func newConfigBackupID(dir string) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var buf [3]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return "", fmt.Errorf("生成配置备份 ID 失败: %w", err)
		}
		id := time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(buf[:])
		if _, err := os.Stat(filepath.Join(dir, id+".yaml")); errors.Is(err, os.ErrNotExist) {
			return id, nil
		}
	}
	return "", errors.New("生成配置备份 ID 失败：重名次数过多")
}

func validConfigBackupID(id string) bool {
	return configBackupIDPattern.MatchString(id)
}

func configSecretFingerprints(data []byte) (salt, jwt string) {
	fc := defaults()
	if err := yaml.Unmarshal(data, fc); err != nil {
		return "", ""
	}
	return shortFingerprint(strings.TrimSpace(fc.Security.Salt)), shortFingerprint(strings.TrimSpace(fc.Security.JWTSecret))
}

func shortFingerprint(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

func readConfigBackupMetas(dataDir string) ([]ConfigBackupMeta, error) {
	dir := ConfigBackupDir(dataDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []ConfigBackupMeta{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置备份目录失败: %w", err)
	}
	out := make([]ConfigBackupMeta, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validConfigBackupID(id) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var meta ConfigBackupMeta
		if err := json.Unmarshal(raw, &meta); err != nil || meta.ID != id {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func pruneConfigBackups(dataDir string) {
	metas, err := readConfigBackupMetas(dataDir)
	if err != nil {
		return
	}
	auto := make([]ConfigBackupMeta, 0, len(metas))
	manual := make([]ConfigBackupMeta, 0, len(metas))
	for _, meta := range metas {
		if meta.Reason == configBackupReasonManual {
			manual = append(manual, meta)
		} else {
			auto = append(auto, meta)
		}
	}
	dir := ConfigBackupDir(dataDir)
	pruneConfigBackupGroup(dir, auto, ConfigBackupAutoRetention)
	pruneConfigBackupGroup(dir, manual, ConfigBackupManualRetention)
}

func pruneConfigBackupGroup(dir string, metas []ConfigBackupMeta, limit int) {
	if limit <= 0 || len(metas) <= limit {
		return
	}
	for _, meta := range metas[limit:] {
		_ = os.Remove(filepath.Join(dir, meta.ID+".yaml"))
		_ = os.Remove(filepath.Join(dir, meta.ID+".json"))
	}
}
