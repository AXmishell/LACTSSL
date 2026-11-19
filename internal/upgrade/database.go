package upgrade

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// DatabaseStats 是数据库文件与 SQLite 内部页面的概况。
type DatabaseStats struct {
	FileSize    int64  `json:"file_size"`
	WALSize     int64  `json:"wal_size"`
	PageSize    int64  `json:"page_size"`
	PageCount   int64  `json:"page_count"`
	FreePages   int64  `json:"free_pages"`
	UsedBytes   int64  `json:"used_bytes"`
	FreeBytes   int64  `json:"free_bytes"`
	JournalMode string `json:"journal_mode"`
	Integrity   string `json:"integrity"`
	Tables      int64  `json:"tables"`
	Indexes     int64  `json:"indexes"`
}

// OptimizeOptions 控制数据库优化的强度。
type OptimizeOptions struct {
	Vacuum         bool
	Analyze        bool
	Checkpoint     bool
	CheckIntegrity bool
}

// OptimizeResult 是数据库优化结果。
type OptimizeResult struct {
	Before         DatabaseStats `json:"before"`
	After          DatabaseStats `json:"after"`
	Actions        []string      `json:"actions"`
	Warnings       []string      `json:"warnings"`
	ReclaimedBytes int64         `json:"reclaimed_bytes"`
	Duration       time.Duration `json:"duration"`
}

// hotIndexes 是调度、证书筛选和日志清理最常用的索引。
// 使用 IF NOT EXISTS 保证重复执行安全；返回实际新建的索引名。
var hotIndexes = []struct {
	name string
	sql  string
}{
	{"idx_certificates_status_not_after", `CREATE INDEX IF NOT EXISTS idx_certificates_status_not_after ON certificates(status, not_after)`},
	{"idx_tasks_enabled_next_run", `CREATE INDEX IF NOT EXISTS idx_tasks_enabled_next_run ON tasks(enabled, next_run_at)`},
	{"idx_monitors_enabled_next_check", `CREATE INDEX IF NOT EXISTS idx_monitors_enabled_next_check ON monitors(enabled, next_check_at)`},
	{"idx_deploy_records_created", `CREATE INDEX IF NOT EXISTS idx_deploy_records_created ON deploy_records(created_at)`},
	{"idx_logs_level_created", `CREATE INDEX IF NOT EXISTS idx_logs_level_created ON logs(level, created_at)`},
	{"idx_acme_orders_expires", `CREATE INDEX IF NOT EXISTS idx_acme_orders_expires ON acme_orders(expires_at)`},
	{"idx_acme_challenges_status_next", `CREATE INDEX IF NOT EXISTS idx_acme_challenges_status_next ON acme_challenges(status, next_check_at)`},
	{"idx_acme_eab_endpoint", `CREATE INDEX IF NOT EXISTS idx_acme_eab_endpoint ON acme_eab_credentials(endpoint_id)`},
}

// EnsureIndexes 补充历史数据库缺少的热路径索引。
func (m *Manager) EnsureIndexes(ctx context.Context) ([]string, error) {
	existing := map[string]bool{}
	indexQuery := `SELECT name FROM sqlite_master WHERE type='index'`
	if m.db.Dialect().IsPostgres() {
		indexQuery = `SELECT indexname FROM pg_indexes WHERE schemaname=current_schema()`
	}
	rows, err := m.db.QueryContext(ctx, indexQuery)
	if err != nil {
		return nil, fmt.Errorf("读取索引列表失败: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	created := make([]string, 0, len(hotIndexes))
	for _, item := range hotIndexes {
		if existing[item.name] {
			continue
		}
		if _, err := m.db.ExecContext(ctx, item.sql); err != nil {
			return created, fmt.Errorf("创建索引 %s 失败: %w", item.name, err)
		}
		created = append(created, item.name)
	}
	return created, nil
}

// Optimize 执行安全的数据库维护：补充索引、更新统计、折叠 WAL，并按需 VACUUM。
func (m *Manager) Optimize(ctx context.Context, opts OptimizeOptions) (OptimizeResult, error) {
	started := time.Now()
	result := OptimizeResult{Actions: []string{}, Warnings: []string{}}

	before, err := m.databaseStats(ctx, opts.CheckIntegrity)
	if err != nil {
		return result, err
	}
	result.Before = before

	if created, indexErr := m.EnsureIndexes(ctx); indexErr != nil {
		result.Warnings = append(result.Warnings, indexErr.Error())
	} else if len(created) > 0 {
		result.Actions = append(result.Actions, "补充索引: "+strings.Join(created, ", "))
	}

	if m.db.Dialect().IsPostgres() {
		// PostgreSQL 由 autovacuum 维护统计信息，这里只执行显式 ANALYZE。
	} else if _, err := m.db.ExecContext(ctx, `PRAGMA optimize`); err != nil {
		result.Warnings = append(result.Warnings, "PRAGMA optimize 失败: "+err.Error())
	} else {
		result.Actions = append(result.Actions, "更新查询规划器统计（PRAGMA optimize）")
	}

	if opts.Analyze {
		if _, err := m.db.ExecContext(ctx, `ANALYZE`); err != nil {
			result.Warnings = append(result.Warnings, "ANALYZE 失败: "+err.Error())
		} else {
			result.Actions = append(result.Actions, "完整分析表和索引（ANALYZE）")
		}
	}

	if opts.Checkpoint && !m.db.Dialect().IsPostgres() {
		var busy, logFrames, checkpointed int
		row := m.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
		if err := row.Scan(&busy, &logFrames, &checkpointed); err != nil {
			// 部分驱动返回单列结果，此时不视为致命错误。
			if _, execErr := m.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); execErr != nil {
				result.Warnings = append(result.Warnings, "WAL checkpoint 失败: "+execErr.Error())
			} else {
				result.Actions = append(result.Actions, "折叠并截断 WAL")
			}
		} else if busy != 0 {
			result.Warnings = append(result.Warnings, "WAL checkpoint 被其他连接占用，等待下次维护重试")
		} else {
			result.Actions = append(result.Actions, "折叠并截断 WAL")
		}
	}

	if opts.Vacuum {
		vacuumCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		vacuumSQL := `VACUUM`
		if m.db.Dialect().IsPostgres() {
			vacuumSQL = `VACUUM (ANALYZE)`
		}
		if _, err := m.db.ExecContext(vacuumCtx, vacuumSQL); err != nil {
			result.Warnings = append(result.Warnings, "VACUUM 失败: "+err.Error())
		} else {
			result.Actions = append(result.Actions, "VACUUM 回收空闲页面")
		}
		// VACUUM 会重写统计信息，再执行一次轻量优化。
		if !m.db.Dialect().IsPostgres() {
			_, _ = m.db.ExecContext(vacuumCtx, `PRAGMA optimize`)
		}
	}

	after, err := m.databaseStats(ctx, opts.CheckIntegrity)
	if err != nil {
		result.Warnings = append(result.Warnings, "优化后统计失败: "+err.Error())
	} else {
		result.After = after
		reclaimed := (before.FileSize + before.WALSize) - (after.FileSize + after.WALSize)
		if reclaimed > 0 {
			result.ReclaimedBytes = reclaimed
		}
	}
	result.Duration = time.Since(started)
	if len(result.Warnings) == 0 {
		result.Warnings = []string{}
	}
	return result, nil
}

// databaseStats 读取 SQLite 页面统计并执行快速完整性检查。
func (m *Manager) databaseStats(ctx context.Context, checkIntegrity bool) (DatabaseStats, error) {
	if m.db.Dialect().IsPostgres() {
		return m.postgresStats(ctx, checkIntegrity)
	}
	stats := DatabaseStats{Integrity: "ok"}
	if m.dbPath != "" {
		if info, err := os.Stat(m.dbPath); err == nil {
			stats.FileSize = info.Size()
		}
		if info, err := os.Stat(m.dbPath + "-wal"); err == nil {
			stats.WALSize = info.Size()
		}
	}

	pragmas := []struct {
		target *int64
		query  string
	}{
		{&stats.PageSize, `PRAGMA page_size`},
		{&stats.PageCount, `PRAGMA page_count`},
		{&stats.FreePages, `PRAGMA freelist_count`},
	}
	for _, item := range pragmas {
		if err := m.db.QueryRowContext(ctx, item.query).Scan(item.target); err != nil {
			return stats, fmt.Errorf("读取 %s 失败: %w", item.query, err)
		}
	}
	stats.UsedBytes = (stats.PageCount - stats.FreePages) * stats.PageSize
	stats.FreeBytes = stats.FreePages * stats.PageSize

	if err := m.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&stats.JournalMode); err != nil {
		stats.JournalMode = "unknown"
	}
	if checkIntegrity {
		var check string
		if err := m.db.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&check); err != nil {
			stats.Integrity = "unavailable"
		} else {
			stats.Integrity = check
		}
	} else {
		stats.Integrity = "skipped"
	}
	_ = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&stats.Tables)
	_ = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_%'`).Scan(&stats.Indexes)
	return stats, nil
}

func (m *Manager) postgresStats(ctx context.Context, checkIntegrity bool) (DatabaseStats, error) {
	stats := DatabaseStats{Integrity: "ok", JournalMode: "wal"}
	if err := m.db.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&stats.FileSize); err != nil {
		return stats, fmt.Errorf("读取数据库大小失败: %w", err)
	}
	var blockSize int64
	if err := m.db.QueryRowContext(ctx, `SELECT current_setting('block_size')::bigint`).Scan(&blockSize); err == nil {
		stats.PageSize = blockSize
		stats.PageCount = stats.FileSize / blockSize
	}
	stats.UsedBytes = stats.FileSize
	if checkIntegrity {
		if err := m.db.QueryRowContext(ctx, `SELECT 1`).Scan(new(int)); err != nil {
			stats.Integrity = "unavailable"
		} else {
			stats.Integrity = "ok"
		}
	} else {
		stats.Integrity = "skipped"
	}
	_ = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_type='BASE TABLE'`).Scan(&stats.Tables)
	_ = m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE schemaname=current_schema()`).Scan(&stats.Indexes)
	return stats, nil
}

// healthError 在快速检查发现损坏时返回错误，供启动流程决定是否继续。
func (m *Manager) healthError(ctx context.Context) error {
	stats, err := m.databaseStats(ctx, true)
	if err != nil {
		return err
	}
	if stats.Integrity != "ok" && stats.Integrity != "unavailable" {
		return fmt.Errorf("数据库完整性检查未通过: %s", stats.Integrity)
	}
	return nil
}
