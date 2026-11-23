package dbconvert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/dbdump"
)

// Preflight 检查源/目标、表结构与预估数据量，不修改任何数据。
func (r *Runner) Preflight(ctx context.Context) (*PreflightReport, error) {
	opts := r.opts
	if opts.SourceDriver == "" && opts.Source != nil {
		opts.SourceDriver = opts.Source.Dialect()
	}
	if opts.SourceDriver == "" {
		opts.SourceDriver = db.SQLite
	}
	if opts.TargetDriver == "" {
		return nil, errors.New("目标数据库驱动不能为空")
	}
	if opts.SourceDriver == opts.TargetDriver {
		if !opts.AllowSameDriver {
			return nil, errors.New("源数据库与目标数据库类型相同；同构迁移需要显式确认")
		}
		if opts.Force {
			return nil, errors.New("同构迁移不允许清空目标库，请使用空目标")
		}
	}
	if opts.SourceDriver == db.SQLite && strings.TrimSpace(opts.SourcePath) == "" {
		return nil, errors.New("缺少 SQLite 源数据库路径")
	}
	if !opts.SourceDriver.IsMySQL() && opts.SourceDriver != db.SQLite && strings.TrimSpace(opts.SourceDSN) == "" {
		return nil, errors.New("缺少 PostgreSQL 源 DSN")
	}
	if opts.SourceDriver.IsMySQL() && strings.TrimSpace(opts.SourceDSN) == "" {
		return nil, errors.New("缺少 MySQL 源 DSN")
	}
	if opts.TargetDriver == db.SQLite && strings.TrimSpace(opts.TargetPath) == "" {
		return nil, errors.New("缺少 SQLite 目标数据库路径")
	}
	if !opts.TargetDriver.IsMySQL() && opts.TargetDriver != db.SQLite && strings.TrimSpace(opts.TargetDSN) == "" {
		return nil, errors.New("缺少 PostgreSQL 目标 DSN")
	}
	if opts.TargetDriver.IsMySQL() && strings.TrimSpace(opts.TargetDSN) == "" {
		return nil, errors.New("缺少 MySQL 目标 DSN")
	}

	source, closeSource, err := r.openSource(ctx)
	if err != nil {
		return nil, err
	}
	defer closeSource()

	version, err := source.SchemaVersion()
	if err != nil {
		return nil, fmt.Errorf("读取源数据库结构版本失败: %w", err)
	}
	if version < db.CurrentSchemaVersion() {
		return nil, fmt.Errorf("源数据库结构版本 v%d 低于当前程序 v%d，请先执行 /upgrade 迁移", version, db.CurrentSchemaVersion())
	}
	if version > db.CurrentSchemaVersion() {
		return nil, fmt.Errorf("源数据库结构版本 v%d 高于当前程序 v%d，请先升级程序", version, db.CurrentSchemaVersion())
	}

	target, closeTarget, err := r.openTarget(ctx, false)
	if err != nil {
		return nil, err
	}
	defer closeTarget()
	if err := ensureDistinctTarget(ctx, source, target, opts.SourceDriver, opts.TargetDriver, opts.SourcePath, opts.TargetPath); err != nil {
		return nil, err
	}

	empty, err := targetEmpty(ctx, target)
	if err != nil {
		return nil, err
	}
	report := &PreflightReport{
		SourceDriver: opts.SourceDriver.String(),
		TargetDriver: opts.TargetDriver.String(),
		TargetEmpty:  empty,
		Tables:       []TableProgress{},
	}
	if !empty {
		if opts.SourceDriver == opts.TargetDriver {
			return nil, errors.New("同构迁移要求目标数据库为空，请先手动清空目标")
		}
		if !opts.Force {
			return nil, errors.New("目标数据库非空；如需清空请显式启用覆盖选项")
		}
	}
	for _, spec := range db.DataTables() {
		ok, err := tableExists(ctx, source, spec.Name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("源数据库缺少表 %s", spec.Name)
		}
		count, err := countTable(ctx, source, spec.Name)
		if err != nil {
			return nil, err
		}
		report.SourceRows += count
		report.Tables = append(report.Tables, TableProgress{Name: spec.Name, Rows: count})
	}
	report.SourceTables = len(report.Tables)
	switch {
	case opts.SourceDriver == db.SQLite:
		if info, err := os.Stat(opts.SourcePath); err == nil {
			report.SourceBytes = info.Size()
		}
	case opts.SourceDriver.IsMySQL():
		_ = source.QueryRowContext(ctx, `SELECT COALESCE(SUM(data_length+index_length),0)
			FROM information_schema.tables WHERE table_schema=DATABASE()`).Scan(&report.SourceBytes)
	default:
		_ = source.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&report.SourceBytes)
	}
	if opts.TargetDriver == db.Postgres {
		var targetVersion int
		if err := target.QueryRowContext(ctx, `SHOW server_version_num`).Scan(&targetVersion); err == nil && targetVersion < 180000 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("目标 PostgreSQL 版本为 %d，建议使用 18", targetVersion))
		}
	}
	if opts.TargetDriver.IsMySQL() {
		var targetVersion string
		if err := target.QueryRowContext(ctx, `SELECT VERSION()`).Scan(&targetVersion); err == nil && !db.SupportsMySQLVersion(targetVersion) {
			report.Warnings = append(report.Warnings, fmt.Sprintf("目标 MySQL 版本为 %s，建议使用 5.7+", targetVersion))
		}
	}
	return report, nil
}

// Run 执行备份、冻结、复制、校验。成功时源库保持冻结，调用方应在切换配置后
// 重启服务；失败时自动解冻并返回错误。
func (r *Runner) Run(ctx context.Context) (*Report, error) {
	opts := r.opts
	started := r.now()
	report := &Report{
		FromDriver: opts.SourceDriver.String(),
		ToDriver:   opts.TargetDriver.String(),
		Phase:      PhaseBackup,
		StartedAt:  started,
		Tables:     []TableProgress{},
	}
	preflight, err := r.Preflight(ctx)
	if err != nil {
		report.Phase = PhaseFailed
		report.Error = err.Error()
		return report, err
	}
	report.Warnings = append(report.Warnings, preflight.Warnings...)

	backupPath, err := r.backupSource(ctx)
	if err != nil {
		report.Phase = PhaseFailed
		report.Error = err.Error()
		return report, err
	}
	report.BackupPath = backupPath

	source, closeSource, err := r.openSource(ctx)
	if err != nil {
		report.Phase = PhaseFailed
		report.Error = err.Error()
		return report, err
	}
	defer closeSource()

	target, closeTarget, err := r.openTarget(ctx, true)
	if err != nil {
		report.Phase = PhaseFailed
		report.Error = err.Error()
		return report, err
	}
	defer closeTarget()
	// 目标 schema 在冻结前创建，尽量缩短写冻结窗口。
	if err := target.Migrate(); err != nil {
		report.Phase = PhaseFailed
		report.Error = err.Error()
		return report, err
	}

	frozen := false
	if opts.Source != nil && opts.Source.Gate() != nil {
		report.Phase = PhaseFreeze
		r.reportProgress(Progress{Phase: PhaseFreeze, StartedAt: started})
		if err := opts.Source.Freeze(ctx); err != nil {
			report.Phase = PhaseFailed
			report.Error = err.Error()
			return report, err
		}
		frozen = true
	}

	report.Phase = PhaseCopy
	tableTotal := len(db.DataTables())
	for index, spec := range db.DataTables() {
		if err := ctx.Err(); err != nil {
			report.Phase = PhaseFailed
			report.Error = err.Error()
			if frozen {
				opts.Source.Unfreeze()
			}
			return report, err
		}
		r.reportProgress(Progress{
			Phase:      PhaseCopy,
			Table:      spec.Name,
			TableIndex: index + 1,
			TableTotal: tableTotal,
			Rows:       report.Rows,
			TotalRows:  preflight.SourceRows,
			StartedAt:  started,
		})
		result, err := copyTable(ctx, source, target, spec, func(rows int64) {
			r.reportProgress(Progress{
				Phase:      PhaseCopy,
				Table:      spec.Name,
				TableIndex: index + 1,
				TableTotal: tableTotal,
				Rows:       report.Rows + rows,
				TotalRows:  preflight.SourceRows,
				Bytes:      report.Bytes + resultBytes(report.Tables),
				StartedAt:  started,
			})
		})
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			report.Tables = append(report.Tables, result)
			report.Phase = PhaseFailed
			report.Error = err.Error()
			if frozen {
				opts.Source.Unfreeze()
			}
			return report, err
		}
		report.Tables = append(report.Tables, result)
		report.Rows += result.Rows
		report.Bytes += result.Bytes
	}

	report.Phase = PhaseVerify
	for index, spec := range db.DataTables() {
		r.reportProgress(Progress{
			Phase:      PhaseVerify,
			Table:      spec.Name,
			TableIndex: index + 1,
			TableTotal: tableTotal,
			Rows:       report.Rows,
			TotalRows:  preflight.SourceRows,
			StartedAt:  started,
		})
		expected := report.Tables[index]
		actual, err := scanTable(ctx, target, spec, expected.columns)
		if err != nil {
			report.Phase = PhaseFailed
			report.Error = err.Error()
			if frozen {
				opts.Source.Unfreeze()
			}
			return report, err
		}
		if actual.Rows != expected.Rows || actual.Hash != expected.Hash {
			report.Phase = PhaseFailed
			err := fmt.Errorf("表 %s 校验失败: 源 %d 行/%s，目标 %d 行/%s",
				spec.Name, expected.Rows, expected.Hash, actual.Rows, actual.Hash)
			report.Error = err.Error()
			if frozen {
				opts.Source.Unfreeze()
			}
			return report, err
		}
		report.Tables[index].Status = "verified"
	}
	if err := resetSequences(ctx, target, db.DataTables()); err != nil {
		report.Phase = PhaseFailed
		report.Error = err.Error()
		if frozen {
			opts.Source.Unfreeze()
		}
		return report, err
	}
	if err := r.verifyEncrypted(ctx, target); err != nil {
		report.Phase = PhaseFailed
		report.Error = err.Error()
		if frozen {
			opts.Source.Unfreeze()
		}
		return report, err
	}
	if version, err := target.SchemaVersion(); err != nil || version != db.CurrentSchemaVersion() {
		report.Phase = PhaseFailed
		report.Error = fmt.Sprintf("目标数据库结构版本异常: %d (%v)", version, err)
		if frozen {
			opts.Source.Unfreeze()
		}
		return report, errors.New(report.Error)
	}

	report.Phase = PhaseSwitching
	report.FinishedAt = r.now()
	r.reportProgress(Progress{Phase: PhaseSwitching, StartedAt: started})
	return report, nil
}

func resultBytes(tables []TableProgress) int64 {
	var total int64
	for _, table := range tables {
		total += table.Bytes
	}
	return total
}

func (r *Runner) openSource(ctx context.Context) (*db.Handle, func(), error) {
	driver := r.opts.SourceDriver
	if driver == "" && r.opts.Source != nil {
		driver = r.opts.Source.Dialect()
	}
	if driver == "" {
		driver = db.SQLite
	}
	var (
		conn *db.Handle
		err  error
	)
	switch driver {
	case db.SQLite:
		conn, err = db.OpenWithOptions(db.Options{
			Driver:    db.SQLite,
			Path:      r.opts.SourcePath,
			Migrate:   false,
			QueryOnly: true,
		})
	case db.Postgres, db.MySQL:
		conn, err = db.OpenWithOptions(db.Options{
			Driver:       driver,
			DSN:          r.opts.SourceDSN,
			Migrate:      false,
			MaxOpenConns: 2,
			MaxIdleConns: 1,
		})
	default:
		return nil, nil, fmt.Errorf("不支持的源数据库驱动: %s", driver)
	}
	if err != nil {
		return nil, nil, err
	}
	return conn, func() { _ = conn.Close() }, nil
}

func (r *Runner) openTarget(ctx context.Context, migrate bool) (*db.Handle, func(), error) {
	driver := r.opts.TargetDriver
	var (
		conn *db.Handle
		err  error
	)
	switch driver {
	case db.SQLite:
		conn, err = db.OpenWithOptions(db.Options{Driver: db.SQLite, Path: r.opts.TargetPath, Migrate: false})
	case db.Postgres, db.MySQL:
		conn, err = db.OpenWithOptions(db.Options{
			Driver: driver, DSN: r.opts.TargetDSN, Migrate: false,
			MaxOpenConns: 4, MaxIdleConns: 2,
		})
	default:
		return nil, nil, fmt.Errorf("不支持的目标数据库驱动: %s", driver)
	}
	if err != nil {
		return nil, nil, err
	}
	if migrate {
		empty, err := targetEmpty(ctx, conn)
		if err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
		if !empty {
			if !r.opts.Force {
				_ = conn.Close()
				return nil, nil, errors.New("目标数据库非空；如需清空请显式启用覆盖选项")
			}
			if err := clearTarget(ctx, conn); err != nil {
				_ = conn.Close()
				return nil, nil, err
			}
		}
	}
	return conn, func() { _ = conn.Close() }, nil
}

// targetEmpty 判断目标 schema 是否没有用户表。
func targetEmpty(ctx context.Context, conn *db.Handle) (bool, error) {
	if conn.Dialect().IsMySQL() {
		var count int64
		err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema=DATABASE() AND table_type='BASE TABLE'`).Scan(&count)
		return count == 0, err
	}
	if conn.Dialect().IsPostgres() {
		var count int64
		err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema=current_schema() AND table_type='BASE TABLE'`).Scan(&count)
		return count == 0, err
	}
	var count int64
	err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&count)
	return count == 0, err
}

// clearTarget 清空当前 schema 的用户表。调用方必须已获得用户明确确认。
func clearTarget(ctx context.Context, conn *db.Handle) error {
	if conn.Dialect().IsMySQL() {
		// 固定使用同一个连接，确保 FOREIGN_KEY_CHECKS 对后续 DROP 生效。
		pinned, err := conn.Conn(ctx)
		if err != nil {
			return err
		}
		defer pinned.Close()
		if _, err := pinned.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS=0`); err != nil {
			return err
		}
		defer func() { _, _ = pinned.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS=1`) }()
		rows, err := pinned.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
			WHERE table_schema=DATABASE() AND table_type='BASE TABLE'`)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				return err
			}
			names = append(names, name)
		}
		_ = rows.Close()
		for _, name := range names {
			if _, err := pinned.ExecContext(ctx, `DROP TABLE IF EXISTS `+quoteIdent(conn.Dialect(), name)); err != nil {
				return err
			}
		}
		return nil
	}
	if conn.Dialect().IsPostgres() {
		rows, err := conn.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
			WHERE table_schema=current_schema() AND table_type='BASE TABLE'`)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				return err
			}
			names = append(names, name)
		}
		_ = rows.Close()
		for _, name := range names {
			if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS `+quoteIdent(conn.Dialect(), name)+` CASCADE`); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`) }()
	rows, err := conn.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		names = append(names, name)
	}
	_ = rows.Close()
	for _, name := range names {
		if _, err := conn.ExecContext(ctx, `DROP TABLE IF EXISTS `+quoteIdent(conn.Dialect(), name)); err != nil {
			return err
		}
	}
	return nil
}

// verifyEncrypted 抽样验证目标库的敏感字段能用当前 salt 解密。
func (r *Runner) verifyEncrypted(ctx context.Context, target *db.Handle) error {
	if r.opts.Cipher == nil {
		return nil
	}
	checks := []struct {
		table  string
		column string
	}{
		{"users", "totp_secret"},
		{"certificates", "key_pem"},
		{"private_cas", "key_pem"},
		{"dns_accounts", "config"},
		{"deploy_accounts", "config"},
		{"acme_cas", "eab_hmac"},
	}
	for _, check := range checks {
		var value string
		query := fmt.Sprintf(`SELECT %s FROM %s WHERE %s LIKE 'enc:v1:%%' LIMIT 1`,
			quoteIdent(target.Dialect(), check.column), quoteIdent(target.Dialect(), check.table), quoteIdent(target.Dialect(), check.column))
		err := target.QueryRowContext(ctx, query).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := r.opts.Cipher.Decrypt(value); err != nil {
			return fmt.Errorf("目标库 %s.%s 解密失败，salt 可能不一致: %w", check.table, check.column, err)
		}
	}
	return nil
}

// backupSource 在复制前备份源库，失败时阻止切换。
func (r *Runner) backupSource(ctx context.Context) (string, error) {
	dir := filepath.Join(r.opts.DataDir, "db-convert", "backups", time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建切换前备份目录失败: %w", err)
	}
	switch {
	case r.opts.SourceDriver == db.SQLite:
		target := filepath.Join(dir, "source.db")
		conn, err := db.OpenWithOptions(db.Options{Driver: db.SQLite, Path: r.opts.SourcePath, Migrate: false})
		if err != nil {
			return dir, err
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, `VACUUM INTO ?`, target); err != nil {
			return dir, fmt.Errorf("备份 SQLite 源库失败: %w", err)
		}
		_ = os.Chmod(target, 0o600)
	case r.opts.SourceDriver.IsMySQL():
		target := filepath.Join(dir, "source.sql")
		conn, err := db.OpenWithOptions(db.Options{Driver: db.MySQL, DSN: r.opts.SourceDSN, Migrate: false})
		if err != nil {
			return dir, fmt.Errorf("连接 MySQL 源库失败: %w", err)
		}
		dumpErr := dbdump.DumpMySQL(ctx, conn.Raw(), target)
		_ = conn.Close()
		if dumpErr != nil {
			return dir, fmt.Errorf("备份 MySQL 源库失败: %w", dumpErr)
		}
		_ = os.Chmod(target, 0o600)
	default:
		target := filepath.Join(dir, "source.dump")
		conn, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: r.opts.SourceDSN, Migrate: false})
		if err != nil {
			return dir, fmt.Errorf("连接 PostgreSQL 源库失败: %w", err)
		}
		dumpErr := dbdump.DumpPostgresFromSQL(ctx, conn.Raw(), target)
		_ = conn.Close()
		if dumpErr != nil {
			return dir, fmt.Errorf("备份 PostgreSQL 源库失败: %w", dumpErr)
		}
		_ = os.Chmod(target, 0o600)
	}
	if r.opts.ConfigPath != "" {
		if data, err := os.ReadFile(r.opts.ConfigPath); err == nil {
			_ = os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600)
		}
	}
	return dir, nil
}
