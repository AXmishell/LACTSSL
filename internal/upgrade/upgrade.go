// Package upgrade 集中处理 LACTSSL 的历史遗留迁移、数据库优化与配置文件优化。
//
// 设计目标：
//  1. 把散落在 main 与各接口中的兼容逻辑收敛为可重复执行、可观测的迁移步骤；
//  2. 升级二进制后由启动流程自动迁移，失败时保留版本号以便下次重试；
//  3. 通过 /upgrade 页面提供预检、手动迁移和数据库 VACUUM 等运维能力；
//  4. 所有步骤幂等，重复执行不会破坏已有数据。
package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"lactssl/internal/config"
	"lactssl/internal/db"
	"lactssl/internal/service"
	"lactssl/internal/store"
)

// CurrentVersion 是当前数据格式版本。升级步骤按版本号顺序执行。
//
// 版本历史：
//   - 0：v1.0.x，无显式版本标记，TOTP/证书私钥可能仍是明文；
//   - 1：v1.1.x，集中配置密钥、引入部署流程与通知渠道迁移；
//   - 2：v1.2.x，部署目标合并、EAB 凭据按 CA 拆分；
//   - 3：插件化阶段，清理旧设置、统一插件启用白名单并补充热路径索引；
//   - 4：日志合并阶段，把 system_logs 迁移到 logs 表（category=runtime）。
const CurrentVersion = 4

const (
	versionSetting    = "upgrade.schema_version"
	appVersionSetting = "upgrade.app_version"
	lastRunSetting    = "upgrade.last_run"
	stepSettingPrefix = "upgrade.step."
)

// Options 构造升级管理器所需的依赖。
type Options struct {
	DB               *db.Handle
	Store            *store.Store
	Manager          *service.Manager
	ConfigPath       string
	DataDir          string
	DBPath           string
	LegacySecretFile string
	AppVersion       string
	Auto             bool
	AutoOptimize     bool
	Vacuum           bool
	// SessionPersistence 为 true 时跨版本升级不清理登录会话。
	SessionPersistence bool
	Logger             *slog.Logger
}

// Manager 是升级工具的运行入口。同一进程内串行执行，避免并发迁移。
type Manager struct {
	db                 *db.Handle
	store              *store.Store
	service            *service.Manager
	configPath         string
	dataDir            string
	dbPath             string
	legacySecretFile   string
	appVersion         string
	auto               bool
	autoOpt            bool
	vacuum             bool
	sessionPersistence bool
	logger             *slog.Logger

	mu sync.Mutex
}

// New 创建升级管理器。
func New(opts Options) (*Manager, error) {
	if opts.DB == nil || opts.Store == nil {
		return nil, fmt.Errorf("升级工具缺少数据库依赖")
	}
	manager := opts.Manager
	if manager == nil {
		manager = service.New(opts.Store)
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		db:                 opts.DB,
		store:              opts.Store,
		service:            manager,
		configPath:         opts.ConfigPath,
		dataDir:            opts.DataDir,
		dbPath:             opts.DBPath,
		legacySecretFile:   opts.LegacySecretFile,
		appVersion:         opts.AppVersion,
		auto:               opts.Auto,
		autoOpt:            opts.AutoOptimize,
		vacuum:             opts.Vacuum,
		sessionPersistence: opts.SessionPersistence,
		logger:             logger,
	}, nil
}

// StepStatus 描述一个迁移步骤的当前状态。
type StepStatus struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	TitleKey    string `json:"title_key"`
	DescKey     string `json:"desc_key"`
	Status      string `json:"status"` // done、pending、skipped、failed
	Changed     bool   `json:"changed,omitempty"`
	Detail      string `json:"detail,omitempty"`
	LastApplied string `json:"last_applied,omitempty"`
}

// Status 是 /upgrade 页面的只读预检结果。
type Status struct {
	AppVersion     string                `json:"app_version"`
	StoredVersion  int                   `json:"stored_version"`
	CurrentVersion int                   `json:"current_version"`
	VersionLabel   string                `json:"version_label"`
	NeedsUpgrade   bool                  `json:"needs_upgrade"`
	AutoEnabled    bool                  `json:"auto_enabled"`
	VacuumAllowed  bool                  `json:"vacuum_allowed"`
	Steps          []StepStatus          `json:"steps"`
	LastRun        *RunRecord            `json:"last_run,omitempty"`
	Database       DatabaseStats         `json:"database"`
	Config         config.OptimizeReport `json:"config"`
	Warnings       []string              `json:"warnings"`
	CheckedAt      time.Time             `json:"checked_at"`
}

// RunOptions 控制一次迁移/优化执行的内容。
type RunOptions struct {
	Migrate        bool   `json:"migrate"`
	Config         bool   `json:"config"`
	Optimize       bool   `json:"optimize"`
	Vacuum         bool   `json:"vacuum"`
	Analyze        bool   `json:"analyze"`
	Checkpoint     bool   `json:"checkpoint"`
	CheckIntegrity bool   `json:"check_integrity"`
	CreateBackup   bool   `json:"create_backup"`
	Source         string `json:"source"`
}

// RunReport 是一次迁移/优化的完整结果，同时写入 upgrade.last_run。
type RunReport struct {
	Source      string                `json:"source"`
	AppVersion  string                `json:"app_version"`
	FromVersion int                   `json:"from_version"`
	ToVersion   int                   `json:"to_version"`
	StartedAt   time.Time             `json:"started_at"`
	FinishedAt  time.Time             `json:"finished_at"`
	Steps       []StepStatus          `json:"steps"`
	Config      config.OptimizeReport `json:"config"`
	Database    OptimizeResult        `json:"database"`
	BackupDir   string                `json:"backup_dir,omitempty"`
	Warnings    []string              `json:"warnings"`
	Error       string                `json:"error,omitempty"`
}

// RunRecord 是持久化的最近一次执行摘要。
type RunRecord struct {
	Source       string    `json:"source"`
	AppVersion   string    `json:"app_version"`
	FromVersion  int       `json:"from_version"`
	ToVersion    int       `json:"to_version"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	StepCount    int       `json:"step_count"`
	ChangedSteps int       `json:"changed_steps"`
	ConfigChange int       `json:"config_changes"`
	Reclaimed    int64     `json:"reclaimed_bytes"`
	Warnings     []string  `json:"warnings,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// Status 返回当前迁移状态、配置预检和数据库概况。
func (m *Manager) Status(ctx context.Context) (*Status, error) {
	stored, err := m.storedVersion()
	if err != nil {
		return nil, err
	}
	last, _ := m.lastRun()
	configReport, configErr := config.OptimizeFile(m.configPath, false)
	dbStats, dbErr := m.databaseStats(ctx, true)
	warnings := append([]string{}, configReport.Warnings...)
	if configErr != nil {
		warnings = append(warnings, "配置预检失败: "+configErr.Error())
	}
	if dbErr != nil {
		warnings = append(warnings, "数据库检查失败: "+dbErr.Error())
	}
	if stored > CurrentVersion {
		warnings = append(warnings, fmt.Sprintf("数据格式版本 v%d 高于当前程序支持的 v%d，请使用更新版本或回滚数据备份", stored, CurrentVersion))
	}
	status := &Status{
		AppVersion:     m.appVersion,
		StoredVersion:  stored,
		CurrentVersion: CurrentVersion,
		VersionLabel:   VersionLabel(stored),
		NeedsUpgrade:   stored < CurrentVersion,
		AutoEnabled:    m.auto,
		VacuumAllowed:  m.vacuum,
		Steps:          m.stepStatuses(stored),
		LastRun:        last,
		Database:       dbStats,
		Config:         configReport,
		Warnings:       warnings,
		CheckedAt:      time.Now().UTC(),
	}
	return status, nil
}

// AutoMigrate 在进程启动时调用。关闭自动迁移时只记录待处理状态，不阻塞服务。
func (m *Manager) AutoMigrate(ctx context.Context) error {
	stored, err := m.storedVersion()
	if err != nil {
		return err
	}
	if !m.auto {
		if stored < CurrentVersion {
			m.logger.Warn("检测到待执行的数据迁移，但 upgrade.auto 已关闭；请登录后访问 /upgrade 手动执行",
				"stored_version", stored, "current_version", CurrentVersion)
		}
		return nil
	}
	if stored > CurrentVersion {
		return fmt.Errorf("数据格式版本 v%d 高于当前程序支持的 v%d，请使用更新版本或回滚数据备份", stored, CurrentVersion)
	}
	appVersion, _ := m.store.GetSetting(appVersionSetting)
	if stored >= CurrentVersion && strings.TrimSpace(appVersion) == strings.TrimSpace(m.appVersion) {
		// 数据格式与程序版本都没有变化时跳过自动迁移，避免每次启动重复解析配置与检查表结构。
		return nil
	}
	report, err := m.Run(ctx, RunOptions{
		Migrate:        true,
		Config:         true,
		Optimize:       m.autoOpt,
		Vacuum:         false,
		Analyze:        false,
		Checkpoint:     true,
		CheckIntegrity: stored < CurrentVersion,
		CreateBackup:   stored < CurrentVersion,
		Source:         "startup",
	})
	if report != nil {
		for _, warning := range report.Warnings {
			m.logger.Warn("升级迁移警告", "warning", warning)
		}
		if report.FromVersion < report.ToVersion {
			m.logger.Info("数据格式升级完成",
				"from_version", report.FromVersion,
				"to_version", report.ToVersion,
				"changed_steps", changedStepCount(report.Steps),
				"config_changes", len(report.Config.Changes),
				"backup_dir", report.BackupDir,
				"duration", report.FinishedAt.Sub(report.StartedAt).String())
		}
	}
	return err
}

// Run 执行迁移、配置优化与数据库优化。迁移步骤失败时返回错误且不推进版本号。
func (m *Manager) Run(ctx context.Context, opts RunOptions) (*RunReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if opts.Source == "" {
		opts.Source = "manual"
	}
	started := time.Now().UTC()
	from, err := m.storedVersion()
	if err != nil {
		return nil, err
	}
	if from > CurrentVersion {
		return nil, fmt.Errorf("数据格式版本 v%d 高于当前程序支持的 v%d，请使用更新版本或回滚数据备份", from, CurrentVersion)
	}
	report := &RunReport{
		Source:      opts.Source,
		AppVersion:  m.appVersion,
		FromVersion: from,
		ToVersion:   from,
		StartedAt:   started,
		Steps:       []StepStatus{},
		Config:      config.OptimizeReport{Path: m.configPath, Changes: []config.ConfigChange{}, Warnings: []string{}},
		Database:    OptimizeResult{},
		Warnings:    []string{},
	}

	if opts.Migrate && opts.CreateBackup {
		backupDir, backupErr := m.createBackup(ctx)
		report.BackupDir = backupDir
		if backupErr != nil {
			// 备份失败不终止迁移，但必须显式暴露，避免用户误以为已有回滚点。
			report.Warnings = append(report.Warnings, "迁移前备份失败: "+backupErr.Error())
		}
	}

	if opts.Migrate {
		statuses := m.stepStatuses(from)
		for index := range steps {
			step := steps[index]
			if step.Version > CurrentVersion {
				continue
			}
			status := statuses[index]
			if status.Status == "done" || status.Status == "skipped" {
				report.Steps = append(report.Steps, status)
				continue
			}
			result, stepErr := step.Run(ctx, m)
			status.Status = "done"
			status.Changed = result.Changed
			status.LastApplied = time.Now().UTC().Format(time.RFC3339)
			status.Detail = ""
			if result.Skipped {
				status.Status = "skipped"
			}
			if len(result.Details) > 0 {
				status.Detail = strings.Join(result.Details, "; ")
			}
			if stepErr != nil {
				status.Status = "failed"
				status.Detail = stepErr.Error()
				report.Steps = append(report.Steps, status)
				report.Error = stepErr.Error()
				report.FinishedAt = time.Now().UTC()
				_ = m.store.SetSetting(stepSettingPrefix+step.ID, status.Status)
				_ = m.saveLastRun(report)
				return report, fmt.Errorf("升级步骤 %s 失败: %w", step.ID, stepErr)
			}
			_ = m.store.SetSetting(stepSettingPrefix+step.ID, status.Status)
			_ = m.store.SetSetting("upgrade.step_at."+step.ID, status.LastApplied)
			report.Steps = append(report.Steps, status)
		}
		if err := m.store.SetSetting(versionSetting, strconv.Itoa(CurrentVersion)); err != nil {
			report.Error = err.Error()
			report.FinishedAt = time.Now().UTC()
			_ = m.saveLastRun(report)
			return report, fmt.Errorf("写入数据格式版本失败: %w", err)
		}
		report.ToVersion = CurrentVersion
	}

	if opts.Config && m.configPath != "" {
		configReport, configErr := config.OptimizeFile(m.configPath, true)
		report.Config = configReport
		if configErr != nil {
			report.Warnings = append(report.Warnings, "配置文件优化失败: "+configErr.Error())
		}
	}

	if opts.Optimize {
		result, optimizeErr := m.Optimize(ctx, OptimizeOptions{
			Vacuum:         opts.Vacuum,
			Analyze:        opts.Analyze,
			Checkpoint:     opts.Checkpoint,
			CheckIntegrity: opts.CheckIntegrity,
		})
		report.Database = result
		if optimizeErr != nil {
			report.Warnings = append(report.Warnings, "数据库优化失败: "+optimizeErr.Error())
		}
	}

	_ = m.store.SetSetting(appVersionSetting, m.appVersion)
	report.FinishedAt = time.Now().UTC()
	if err := m.saveLastRun(report); err != nil {
		report.Warnings = append(report.Warnings, "保存升级记录失败: "+err.Error())
	}
	return report, nil
}

// storedVersion 读取显式版本；旧数据库没有版本标记时按 0 处理，
// 让所有幂等步骤完整执行一次，避免依赖零散迁移键推断而漏掉历史清理。
func (m *Manager) storedVersion() (int, error) {
	raw, err := m.store.GetSetting(versionSetting)
	if err != nil {
		return 0, err
	}
	if value, parseErr := strconv.Atoi(strings.TrimSpace(raw)); parseErr == nil && value > 0 {
		return value, nil
	}
	return 0, nil
}

func (m *Manager) stepStatuses(stored int) []StepStatus {
	out := make([]StepStatus, 0, len(steps))
	for _, step := range steps {
		status := StepStatus{
			ID:       step.ID,
			Version:  step.Version,
			TitleKey: step.TitleKey,
			DescKey:  step.DescKey,
			Status:   "pending",
		}
		if raw, _ := m.store.GetSetting(stepSettingPrefix + step.ID); raw == "done" || raw == "skipped" || raw == "failed" {
			status.Status = raw
		} else if stored >= step.Version {
			status.Status = "done"
		}
		if applied, _ := m.store.GetSetting("upgrade.step_at." + step.ID); applied != "" {
			status.LastApplied = applied
		}
		out = append(out, status)
	}
	return out
}

func (m *Manager) lastRun() (*RunRecord, error) {
	raw, err := m.store.GetSetting(lastRunSetting)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	var record RunRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (m *Manager) saveLastRun(report *RunReport) error {
	record := RunRecord{
		Source:       report.Source,
		AppVersion:   report.AppVersion,
		FromVersion:  report.FromVersion,
		ToVersion:    report.ToVersion,
		StartedAt:    report.StartedAt,
		FinishedAt:   report.FinishedAt,
		StepCount:    len(report.Steps),
		ChangedSteps: changedStepCount(report.Steps),
		ConfigChange: len(report.Config.Changes),
		Reclaimed:    report.Database.ReclaimedBytes,
		Warnings:     report.Warnings,
		Error:        report.Error,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return m.store.SetSetting(lastRunSetting, string(data))
}

func changedStepCount(statuses []StepStatus) int {
	count := 0
	for _, status := range statuses {
		if status.Changed {
			count++
		}
	}
	return count
}

// VacuumAllowed 返回配置是否允许手动优化执行 VACUUM。
func (m *Manager) VacuumAllowed() bool { return m.vacuum }

// AutoEnabled 返回是否启用启动时自动迁移。
func (m *Manager) AutoEnabled() bool { return m.auto }

// VersionLabel 把内部版本号转换为便于展示的说明。
func VersionLabel(version int) string {
	switch {
	case version <= 0:
		return "历史版本（未标记）"
	case version < CurrentVersion:
		return fmt.Sprintf("旧版本 v%d", version)
	default:
		return fmt.Sprintf("当前版本 v%d", version)
	}
}

// createBackup 在任何结构变更前生成可回滚的数据库快照与配置副本。
func (m *Manager) createBackup(ctx context.Context) (string, error) {
	if m.dataDir == "" {
		return "", fmt.Errorf("缺少数据目录")
	}
	dir := filepath.Join(m.dataDir, "upgrade-backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return filepath.Join(dir, time.Now().UTC().Format("20060102-150405")), fmt.Errorf("创建备份目录失败: %w", err)
	}
	targetDir, err := os.MkdirTemp(dir, time.Now().UTC().Format("20060102-150405")+"-")
	if err != nil {
		return dir, fmt.Errorf("创建备份目录失败: %w", err)
	}
	_ = os.Chmod(targetDir, 0o700)
	if m.db.Dialect().IsMySQL() {
		target := filepath.Join(targetDir, "lactssl.sql")
		mysqlDump := findMySQLDumpBinary()
		if mysqlDump == "" {
			return targetDir, fmt.Errorf("备份 MySQL 失败: 未找到 mysqldump")
		}
		connection, err := db.MySQLCLI(m.db.DSN())
		if err != nil {
			return targetDir, fmt.Errorf("备份 MySQL 失败: %w", err)
		}
		cmd := exec.CommandContext(ctx, mysqlDump, db.MySQLDumpArgs(mysqlDump, connection, target)...) // #nosec G204 -- mysqldump 从 PATH 解析，参数由服务端生成
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+connection.Password)
		if output, err := cmd.CombinedOutput(); err != nil {
			return targetDir, fmt.Errorf("备份 MySQL 失败: %w: %s", err, strings.TrimSpace(string(output)))
		}
		_ = os.Chmod(target, 0o600)
	} else if m.db.Dialect().IsPostgres() {
		target := filepath.Join(targetDir, "lactssl.dump")
		pgDump := findPgDumpBinary()
		if pgDump == "" {
			return targetDir, fmt.Errorf("备份 PostgreSQL 失败: 未找到 pg_dump")
		}
		safeDSN, pgEnv := db.PostgresCLIEnv(m.db.DSN())
		cmd := exec.CommandContext(ctx, pgDump, "--format=custom", "--no-owner", "--no-privileges", "--file", target, safeDSN) // #nosec G204 -- pg_dump 从 PATH 解析，参数由服务端生成
		cmd.Env = append(os.Environ(), pgEnv...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return targetDir, fmt.Errorf("备份 PostgreSQL 失败: %w: %s", err, strings.TrimSpace(string(output)))
		}
		_ = os.Chmod(target, 0o600)
	} else {
		target := filepath.Join(targetDir, "lactssl.db")
		if _, err := m.db.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
			// 部分 SQLite 构建不接受参数化 VACUUM INTO，退化为转义字面量。
			quoted := strings.ReplaceAll(target, "'", "''")
			if _, retryErr := m.db.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); retryErr != nil {
				return targetDir, fmt.Errorf("备份数据库失败: %w", err)
			}
		}
		_ = os.Chmod(target, 0o600)
	}
	if m.configPath != "" {
		if data, err := os.ReadFile(m.configPath); err == nil {
			configTarget := filepath.Join(targetDir, "config.yaml")
			if err := os.WriteFile(configTarget, data, 0o600); err != nil {
				return targetDir, fmt.Errorf("备份配置文件失败: %w", err)
			}
		}
	}
	m.pruneBackups(dir, 5)
	return targetDir, nil
}

func findPgDumpBinary() string {
	if path, err := exec.LookPath("pg_dump"); err == nil {
		return path
	}
	for _, candidate := range []string{"/usr/pgsql-18/bin/pg_dump", "/usr/bin/pg_dump", "/usr/local/bin/pg_dump"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func findMySQLDumpBinary() string {
	if path, err := exec.LookPath("mysqldump"); err == nil {
		return path
	}
	for _, candidate := range []string{"/usr/bin/mysqldump", "/usr/local/bin/mysqldump", "/usr/local/mysql/bin/mysqldump"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func (m *Manager) pruneBackups(parent string, keep int) {
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) <= keep {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	// 目录名是 UTC 时间戳，按字典序即可确定新旧。
	for i := 0; i < len(names)-keep; i++ {
		_ = os.RemoveAll(filepath.Join(parent, names[i]))
	}
}
