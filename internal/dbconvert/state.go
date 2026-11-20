package dbconvert

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State 是持久化的切换状态，供进程重启后继续自检或回滚。
type State struct {
	ID            string     `json:"id"`
	Phase         Phase      `json:"phase"`
	FromDriver    string     `json:"from_driver"`
	ToDriver      string     `json:"to_driver"`
	FromPath      string     `json:"from_path,omitempty"`
	ToPath        string     `json:"to_path,omitempty"`
	ToDSNSummary  string     `json:"to_dsn_summary,omitempty"`
	BackupPath    string     `json:"backup_path,omitempty"`
	ConfigBackup  string     `json:"config_backup,omitempty"`
	TargetCreated bool       `json:"target_created"`
	SourceRemoved bool       `json:"source_removed,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Error         string     `json:"error,omitempty"`
	Report        *Report    `json:"report,omitempty"`
}

// StatePath 返回切换状态文件路径。
func StatePath(dataDir string) string {
	return filepath.Join(dataDir, "db-convert", "state.json")
}

// LoadState 读取切换状态；不存在时返回 nil。
func LoadState(dataDir string) (*State, error) {
	data, err := os.ReadFile(StatePath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// SaveState 原子写入切换状态。
func SaveState(dataDir string, state *State) error {
	path := StatePath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// ClearState 删除切换状态。
func ClearState(dataDir string) error {
	return os.Remove(StatePath(dataDir))
}

// DSNSummary 对 DSN 做脱敏，只保留 host/database/sslmode 便于页面展示。
func DSNSummary(dsn string) string {
	if strings.TrimSpace(dsn) == "" {
		return ""
	}
	if parsed, err := url.Parse(dsn); err == nil && parsed.Scheme != "" {
		// 直接去掉用户信息，避免 *** 被 URL 编码成 %2A%2A%2A。
		parsed.User = nil
		return parsed.String()
	}
	// 非 URL 形式（key=value）无法可靠脱敏，直接隐藏。
	return "***"
}
