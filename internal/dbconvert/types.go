// Package dbconvert 实现 SQLite 与 PostgreSQL 之间的逻辑数据库切换。
package dbconvert

import (
	"context"
	"log/slog"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/util"
)

// Phase 是切换状态机的阶段。
type Phase string

const (
	PhaseIdle       Phase = "idle"
	PhasePreflight  Phase = "preflight"
	PhaseBackup     Phase = "backup"
	PhaseFreeze     Phase = "freeze"
	PhaseCopy       Phase = "copy"
	PhaseVerify     Phase = "verify"
	PhaseSwitching  Phase = "switching"
	PhaseCompleted  Phase = "completed"
	PhaseFailed     Phase = "failed"
	PhaseRolledBack Phase = "rolled_back"
)

// Options 描述一次切换。
type Options struct {
	// Source 是当前运行中的数据库连接；调用方负责在 Run 前/期间冻结写入。
	Source *db.Handle
	// SourcePath / SourceDSN 用于打开独立的只读复制连接和生成备份。
	SourceDriver db.Dialect
	SourcePath   string
	SourceDSN    string

	TargetDriver db.Dialect
	TargetPath   string
	TargetDSN    string
	// Force 表示目标库非空时清空当前 schema 中的表。默认必须为空。
	Force bool
	// AllowSameDriver 允许同构迁移（SQLite→SQLite、PostgreSQL→PostgreSQL）。
	// 开启后仍要求目标与源不是同一个数据库/文件，且目标必须为空。
	AllowSameDriver bool

	DataDir    string
	ConfigPath string
	Cipher     *util.Cipher
	Version    string

	Logger   *slog.Logger
	Progress func(Progress)
}

// Progress 是前端可轮询的进度快照。
type Progress struct {
	Phase      Phase           `json:"phase"`
	Table      string          `json:"table,omitempty"`
	TableIndex int             `json:"table_index"`
	TableTotal int             `json:"table_total"`
	Rows       int64           `json:"rows"`
	TotalRows  int64           `json:"total_rows"`
	Bytes      int64           `json:"bytes"`
	StartedAt  time.Time       `json:"started_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Warnings   []string        `json:"warnings,omitempty"`
	Tables     []TableProgress `json:"tables,omitempty"`
}

// TableProgress 描述单表复制/校验结果。
type TableProgress struct {
	Name   string `json:"name"`
	Rows   int64  `json:"rows"`
	Bytes  int64  `json:"bytes"`
	Hash   string `json:"hash,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Report 是切换结果。
type Report struct {
	FromDriver string          `json:"from_driver"`
	ToDriver   string          `json:"to_driver"`
	Phase      Phase           `json:"phase"`
	BackupPath string          `json:"backup_path"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Tables     []TableProgress `json:"tables"`
	Rows       int64           `json:"rows"`
	Bytes      int64           `json:"bytes"`
	Warnings   []string        `json:"warnings,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// PreflightReport 是切换前的只读检查结果。
type PreflightReport struct {
	SourceDriver string          `json:"source_driver"`
	TargetDriver string          `json:"target_driver"`
	SourceTables int             `json:"source_tables"`
	TargetEmpty  bool            `json:"target_empty"`
	SourceRows   int64           `json:"source_rows"`
	SourceBytes  int64           `json:"source_bytes"`
	Tables       []TableProgress `json:"tables"`
	Warnings     []string        `json:"warnings,omitempty"`
}

// Runner 是带进度回调的切换执行器。
type Runner struct {
	opts Options
	now  func() time.Time
}

// NewRunner 创建切换执行器。
func NewRunner(opts Options) *Runner {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.SourceDriver == "" && opts.Source != nil {
		opts.SourceDriver = opts.Source.Dialect()
	}
	if opts.SourceDriver == "" {
		opts.SourceDriver = db.SQLite
	}
	return &Runner{opts: opts, now: func() time.Time { return time.Now().UTC() }}
}

func (r *Runner) reportProgress(p Progress) {
	if r.opts.Progress == nil {
		return
	}
	p.UpdatedAt = r.now()
	r.opts.Progress(p)
}

// ctx 检查取消。
func (r *Runner) ctxErr(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
