package dbconvert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lactssl/internal/config"
	"lactssl/internal/db"
	"lactssl/internal/util"
)

// ErrBusy 表示已有转换任务正在执行。
var ErrBusy = errors.New("已有转换任务正在进行")

// StartRequest 是 Web 端发起转换的参数。
type StartRequest struct {
	TargetDriver db.Dialect `json:"-"`
	TargetPath   string     `json:"target_path"`
	TargetDSN    string     `json:"target_dsn"`
	Force        bool       `json:"force"`
}

// Manager 管理一次异步转换任务，供 /upgrade 页面轮询。
type Manager struct {
	Source       *db.Handle
	SourceDriver db.Dialect
	SourcePath   string
	SourceDSN    string
	DataDir      string
	ConfigPath   string
	Cipher       *util.Cipher
	Version      string

	// OnSwitching 在配置改写完成后调用，由 main 注入以触发自动重启。
	OnSwitching func()
	// OnMaintenance 通知 API 层进入/退出维护模式。
	OnMaintenance func(bool)

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	state    *State
	progress Progress
	report   *Report
}

// Status 是 /upgrade 页面需要的任务状态。
type Status struct {
	Running    bool       `json:"running"`
	State      *State     `json:"state,omitempty"`
	Progress   Progress   `json:"progress"`
	Report     *Report    `json:"report,omitempty"`
	SourceInfo SourceInfo `json:"source"`
}

// SourceInfo 是当前数据库的只读展示信息。
type SourceInfo struct {
	Driver string `json:"driver"`
	Path   string `json:"path,omitempty"`
	DSN    string `json:"dsn,omitempty"`
}

// CurrentStatus 返回当前任务状态。
func (m *Manager) CurrentStatus() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	var state *State
	if m.state != nil {
		copied := *m.state
		state = &copied
	}
	var report *Report
	if m.report != nil {
		copied := *m.report
		report = &copied
	}
	status := Status{
		Running:  m.running,
		State:    state,
		Progress: m.progress,
		Report:   report,
		SourceInfo: SourceInfo{
			Driver: m.SourceDriver.String(),
			Path:   m.SourcePath,
			DSN:    DSNSummary(m.SourceDSN),
		},
	}
	return status
}

// TestTarget 验证目标连接、版本与是否为空库。
func (m *Manager) TestTarget(ctx context.Context, req StartRequest) (map[string]any, error) {
	if err := m.validateTarget(req); err != nil {
		return nil, err
	}
	conn, err := m.openTarget(ctx, req, false)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	result := map[string]any{"driver": req.TargetDriver.String()}
	empty, err := targetEmpty(ctx, conn)
	if err != nil {
		return nil, err
	}
	result["empty"] = empty
	if req.TargetDriver == db.Postgres {
		var version string
		if err := conn.QueryRowContext(ctx, `SHOW server_version`).Scan(&version); err == nil {
			result["server_version"] = version
		}
	}
	if req.TargetDriver == db.SQLite {
		result["path"] = req.TargetPath
	} else {
		result["dsn"] = DSNSummary(req.TargetDSN)
	}
	return result, nil
}

// Preflight 生成转换前检查报告。
func (m *Manager) Preflight(ctx context.Context, req StartRequest) (*PreflightReport, error) {
	if err := m.validateTarget(req); err != nil {
		return nil, err
	}
	runner := m.runner(req, nil)
	return runner.Preflight(ctx)
}

// Start 启动异步转换。成功后会改写配置并调用 OnSwitching。
func (m *Manager) Start(req StartRequest) (*State, error) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil, ErrBusy
	}
	if err := m.validateTarget(req); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.running = true
	m.cancel = cancel
	m.report = nil
	m.progress = Progress{Phase: PhasePreflight, StartedAt: time.Now().UTC()}
	state := &State{
		ID:            time.Now().UTC().Format("20060102-150405.000000"),
		Phase:         PhasePreflight,
		FromDriver:    m.SourceDriver.String(),
		ToDriver:      req.TargetDriver.String(),
		FromPath:      m.SourcePath,
		ToPath:        req.TargetPath,
		ToDSNSummary:  DSNSummary(req.TargetDSN),
		TargetCreated: true,
		StartedAt:     time.Now().UTC(),
	}
	m.state = state
	m.mu.Unlock()
	if m.OnMaintenance != nil {
		m.OnMaintenance(true)
	}
	if err := SaveState(m.DataDir, state); err != nil {
		m.finishFailed(err)
		return state, err
	}
	go m.run(ctx, req, state)
	return state, nil
}

// NewManager 创建转换任务管理器。
func NewManager(source *db.Handle, sourceDriver db.Dialect, sourcePath, sourceDSN, dataDir, configPath string, cipher *util.Cipher, version string) *Manager {
	if sourceDriver == "" && source != nil {
		sourceDriver = source.Dialect()
	}
	if sourceDriver == "" {
		sourceDriver = db.SQLite
	}
	manager := &Manager{
		Source:       source,
		SourceDriver: sourceDriver,
		SourcePath:   sourcePath,
		SourceDSN:    sourceDSN,
		DataDir:      dataDir,
		ConfigPath:   configPath,
		Cipher:       cipher,
		Version:      version,
	}
	if state, err := LoadState(dataDir); err == nil && state != nil {
		manager.state = state
		manager.report = state.Report
	}
	return manager
}

func (m *Manager) validateTarget(req StartRequest) error {
	if req.TargetDriver == "" {
		return errors.New("目标数据库驱动不能为空")
	}
	if req.TargetDriver == m.SourceDriver {
		return errors.New("目标数据库与当前数据库类型相同")
	}
	if os.Getenv("LACTSSL_DATABASE_DSN") != "" {
		return errors.New("检测到 LACTSSL_DATABASE_DSN 环境变量；请先移除该变量并重启，否则配置切换不会生效")
	}
	if req.TargetDriver == db.SQLite {
		if req.TargetPath == "" {
			return errors.New("SQLite 目标路径不能为空")
		}
		if m.SourceDriver == db.SQLite {
			sourceAbs, _ := filepath.Abs(m.SourcePath)
			targetAbs, _ := filepath.Abs(req.TargetPath)
			if sourceAbs == targetAbs {
				return errors.New("目标 SQLite 路径不能与当前数据库相同")
			}
		}
	}
	return nil
}

func (m *Manager) runner(req StartRequest, progress func(Progress)) *Runner {
	return NewRunner(Options{
		Source:       m.Source,
		SourceDriver: m.SourceDriver,
		SourcePath:   m.SourcePath,
		SourceDSN:    m.SourceDSN,
		TargetDriver: req.TargetDriver,
		TargetPath:   req.TargetPath,
		TargetDSN:    req.TargetDSN,
		Force:        req.Force,
		DataDir:      m.DataDir,
		ConfigPath:   m.ConfigPath,
		Cipher:       m.Cipher,
		Version:      m.Version,
		Progress:     progress,
	})
}

func (m *Manager) openTarget(ctx context.Context, req StartRequest, migrate bool) (*db.Handle, error) {
	switch req.TargetDriver {
	case db.SQLite:
		return db.OpenWithOptions(db.Options{Driver: db.SQLite, Path: req.TargetPath, Migrate: migrate})
	case db.Postgres:
		return db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: req.TargetDSN, Migrate: migrate, MaxOpenConns: 4, MaxIdleConns: 2})
	default:
		return nil, fmt.Errorf("不支持的目标驱动: %s", req.TargetDriver)
	}
}

func (m *Manager) run(ctx context.Context, req StartRequest, state *State) {
	runner := m.runner(req, func(p Progress) {
		m.mu.Lock()
		m.progress = p
		if state != nil {
			state.Phase = p.Phase
		}
		m.mu.Unlock()
	})
	report, err := runner.Run(ctx)
	m.mu.Lock()
	m.report = report
	state.Report = report
	if report != nil {
		state.Phase = report.Phase
	}
	if err != nil {
		m.running = false
		m.cancel = nil
		state.Phase = PhaseFailed
		state.Error = err.Error()
		now := time.Now().UTC()
		state.FinishedAt = &now
		_ = SaveState(m.DataDir, state)
		if m.Source != nil {
			m.Source.Unfreeze()
		}
		if m.OnMaintenance != nil {
			m.OnMaintenance(false)
		}
		m.mu.Unlock()
		return
	}
	// 转换成功：写配置，保持源库冻结，随后由 main 重启。
	dsn, path := "", ""
	if req.TargetDriver == db.Postgres {
		dsn = req.TargetDSN
	} else {
		path = req.TargetPath
	}
	configBackup, configErr := config.RewriteDatabase(m.ConfigPath, req.TargetDriver.String(), dsn, path)
	if configErr != nil {
		m.running = false
		m.cancel = nil
		state.Phase = PhaseFailed
		state.Error = "改写配置失败: " + configErr.Error()
		now := time.Now().UTC()
		state.FinishedAt = &now
		_ = SaveState(m.DataDir, state)
		if m.Source != nil {
			m.Source.Unfreeze()
		}
		if m.OnMaintenance != nil {
			m.OnMaintenance(false)
		}
		m.mu.Unlock()
		return
	}
	state.Phase = PhaseSwitching
	state.ConfigBackup = configBackup
	state.Report = report
	state.FinishedAt = nil
	_ = SaveState(m.DataDir, state)
	callback := m.OnSwitching
	m.mu.Unlock()
	if callback != nil {
		go func() {
			time.Sleep(time.Second)
			callback()
		}()
	}
}

func (m *Manager) finishFailed(err error) {
	m.mu.Lock()
	m.running = false
	m.cancel = nil
	if m.state != nil {
		m.state.Phase = PhaseFailed
		m.state.Error = err.Error()
		now := time.Now().UTC()
		m.state.FinishedAt = &now
		_ = SaveState(m.DataDir, m.state)
	}
	m.mu.Unlock()
	if m.Source != nil {
		m.Source.Unfreeze()
	}
	if m.OnMaintenance != nil {
		m.OnMaintenance(false)
	}
}

// Rollback 用当前数据库的数据反向转换回转换前的后端（一键回滚）。
// 它会覆盖旧后端中的同名数据表，因此要求旧配置备份存在。
func (m *Manager) Rollback() (*State, error) {
	m.mu.Lock()
	state := m.state
	running := m.running
	m.mu.Unlock()
	if running {
		return nil, ErrBusy
	}
	if state == nil || state.Phase != PhaseCompleted {
		return nil, errors.New("当前没有可回滚的已完成转换")
	}
	if state.ConfigBackup == "" {
		return nil, errors.New("缺少转换前配置备份，无法一键回滚")
	}
	settings, err := config.DatabaseFromFile(state.ConfigBackup)
	if err != nil {
		return nil, fmt.Errorf("读取转换前配置失败: %w", err)
	}
	driver, ok := db.ParseDialect(settings.Driver)
	if !ok || driver == "" {
		return nil, errors.New("转换前数据库类型无效")
	}
	if m.SourceDriver == db.Postgres {
		// 当前是 PG 源，旧配置的 DSN 不能为空。
		if driver == db.Postgres && settings.DSN == "" {
			return nil, errors.New("转换前 PostgreSQL DSN 缺失")
		}
	}
	req := StartRequest{
		TargetDriver: driver,
		TargetDSN:    settings.DSN,
		Force:        true,
	}
	if driver == db.SQLite {
		req.TargetPath = settings.Path
	}
	return m.Start(req)
}

// Cancel 取消尚未切换的转换。
func (m *Manager) Cancel() error {
	m.mu.Lock()
	if !m.running || m.cancel == nil {
		m.mu.Unlock()
		return errors.New("没有可取消的转换任务")
	}
	if m.state != nil && (m.state.Phase == PhaseSwitching || m.state.Phase == PhaseCompleted) {
		m.mu.Unlock()
		return errors.New("转换已进入切换阶段，无法取消")
	}
	cancel := m.cancel
	m.mu.Unlock()
	cancel()
	return nil
}

// VerifyStarted 在重启后校验新库，并返回是否成功。
func VerifyStarted(ctx context.Context, conn *db.Handle, cipher *util.Cipher) error {
	if conn == nil {
		return errors.New("数据库连接为空")
	}
	for _, table := range []string{"users", "settings", "certificates", "tasks", "logs"} {
		var count int64
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(table)).Scan(&count); err != nil {
			return fmt.Errorf("目标库缺少表 %s 或无法读取: %w", table, err)
		}
	}
	if conn.Dialect().IsPostgres() {
		var version string
		if err := conn.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key='schema.version'`).Scan(&version); err != nil {
			return fmt.Errorf("目标库缺少结构版本: %w", err)
		}
	} else {
		version, err := conn.SchemaVersion()
		if err != nil {
			return err
		}
		if version != db.CurrentSchemaVersion() {
			return fmt.Errorf("目标库结构版本为 %d，期望 %d", version, db.CurrentSchemaVersion())
		}
	}
	if cipher != nil {
		checks := []struct{ table, column string }{
			{"users", "totp_secret"},
			{"certificates", "key_pem"},
			{"private_cas", "key_pem"},
			{"dns_accounts", "config"},
			{"deploy_accounts", "config"},
			{"acme_cas", "eab_hmac"},
		}
		for _, check := range checks {
			var value string
			query := `SELECT ` + quoteIdent(check.column) + ` FROM ` + quoteIdent(check.table) +
				` WHERE ` + quoteIdent(check.column) + ` LIKE 'enc:v1:%' LIMIT 1`
			err := conn.QueryRowContext(ctx, query).Scan(&value)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if _, err := cipher.Decrypt(value); err != nil {
				return fmt.Errorf("目标库 %s.%s 解密失败: %w", check.table, check.column, err)
			}
		}
	}
	return nil
}
