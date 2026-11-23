package dbdump

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"lactssl/internal/db"
)

type pgManifest struct {
	Format string    `json:"format"`
	Schema string    `json:"schema"`
	Tables []pgTable `json:"tables"`
}

type pgTable struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Entry   string   `json:"entry"`
}

type pgSequences struct {
	Statements []string `json:"statements"`
}

// DumpPostgresFromSQL 从 database/sql 连接池借出一条 pgx 连接并导出原生备份。
func DumpPostgresFromSQL(ctx context.Context, sqlDB *sql.DB, target string) error {
	if sqlDB == nil {
		return errors.New("PostgreSQL 连接为空")
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取 PostgreSQL 连接失败: %w", err)
	}
	defer conn.Close()
	return conn.Raw(func(driverConn any) error {
		std, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return errors.New("PostgreSQL 驱动不是 pgx stdlib")
		}
		return DumpPostgres(ctx, std.Conn(), target)
	})
}

// DumpPostgres 使用当前连接导出 schema、数据和序列状态。
func DumpPostgres(ctx context.Context, conn *pgx.Conn, target string) error {
	if conn == nil {
		return errors.New("PostgreSQL 连接为空")
	}
	if strings.TrimSpace(target) == "" {
		return errors.New("PostgreSQL 备份路径为空")
	}
	// PostgreSQL 的 COPY 和多条元数据查询需要在同一个快照内完成。
	if _, err := conn.Exec(ctx, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return fmt.Errorf("开始 PostgreSQL 备份事务失败: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "ROLLBACK")
	}()

	var schema string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return fmt.Errorf("读取当前 PostgreSQL schema 失败: %w", err)
	}
	if schema == "" {
		schema = "public"
	}

	tables, err := postgresTables(ctx, conn, schema)
	if err != nil {
		return err
	}
	manifest := pgManifest{Format: PostgresFormat, Schema: schema, Tables: tables}
	sequenceStatements, err := postgresSequenceStatements(ctx, conn, schema)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("创建 PostgreSQL 备份失败: %w", err)
	}
	zw := zip.NewWriter(file)
	closeWithError := func(cause error) error {
		_ = zw.Close()
		_ = file.Close()
		return cause
	}

	if err := writeJSONEntry(zw, "manifest.json", manifest); err != nil {
		return closeWithError(err)
	}
	if err := writeJSONEntry(zw, "sequences.json", pgSequences{Statements: sequenceStatements}); err != nil {
		return closeWithError(err)
	}

	for _, table := range manifest.Tables {
		if len(table.Columns) == 0 {
			continue
		}
		entry, err := zw.CreateHeader(&zip.FileHeader{Name: table.Entry, Method: zip.Store})
		if err != nil {
			return closeWithError(fmt.Errorf("创建表备份条目失败: %w", err))
		}
		copySQL := "COPY " + pgx.Identifier{table.Name}.Sanitize() + " (" + quotedColumns(table.Columns) + ") TO STDOUT"
		if _, err := conn.PgConn().CopyTo(ctx, entry, copySQL); err != nil {
			return closeWithError(fmt.Errorf("导出表 %s 失败: %w", table.Name, err))
		}
	}
	if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
		return closeWithError(fmt.Errorf("提交 PostgreSQL 备份事务失败: %w", err))
	}

	if err := zw.Close(); err != nil {
		_ = file.Close()
		return fmt.Errorf("关闭 PostgreSQL 备份失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭 PostgreSQL 备份文件失败: %w", err)
	}
	return nil
}

func postgresTables(ctx context.Context, conn *pgx.Conn, schema string) ([]pgTable, error) {
	rows, err := conn.Query(ctx, `SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = $1 AND table_type = 'BASE TABLE' AND table_name <> 'schema_meta'
		ORDER BY table_name`, schema)
	if err != nil {
		return nil, fmt.Errorf("读取 PostgreSQL 表列表失败: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("读取 PostgreSQL 表名失败: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取 PostgreSQL 表列表失败: %w", err)
	}

	tables := make([]pgTable, 0, len(names))
	for index, name := range names {
		columns, err := postgresColumns(ctx, conn, schema, name)
		if err != nil {
			return nil, err
		}
		tables = append(tables, pgTable{
			Name:    name,
			Columns: columns,
			Entry:   path.Join("tables", fmt.Sprintf("%04d.copy", index)),
		})
	}
	return tables, nil
}

func postgresColumns(ctx context.Context, conn *pgx.Conn, schema, table string) ([]string, error) {
	rows, err := conn.Query(ctx, `SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND is_generated = 'NEVER'
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("读取表 %s 列信息失败: %w", table, err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("读取表 %s 列名失败: %w", table, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取表 %s 列信息失败: %w", table, err)
	}
	return columns, nil
}

func postgresSequenceStatements(ctx context.Context, conn *pgx.Conn, schema string) ([]string, error) {
	rows, err := conn.Query(ctx, `SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = $1
		  AND (is_identity = 'YES' OR column_default LIKE 'nextval(%')
		ORDER BY table_name, ordinal_position`, schema)
	if err != nil {
		return nil, fmt.Errorf("读取 PostgreSQL 序列信息失败: %w", err)
	}
	defer rows.Close()

	var statements []string
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, fmt.Errorf("读取 PostgreSQL 序列信息失败: %w", err)
		}
		tableIdent := pgx.Identifier{schema, table}.Sanitize()
		columnIdent := pgx.Identifier{column}.Sanitize()
		tableLiteral := quoteLiteral(tableIdent)
		columnLiteral := quoteLiteral(column)
		statements = append(statements, fmt.Sprintf(
			"SELECT setval(pg_get_serial_sequence(%s, %s), COALESCE((SELECT MAX(%s) FROM %s), 0) + 1, false) WHERE pg_get_serial_sequence(%s, %s) IS NOT NULL",
			tableLiteral, columnLiteral, columnIdent, tableIdent, tableLiteral, columnLiteral))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取 PostgreSQL 序列信息失败: %w", err)
	}
	return statements, nil
}

// RestorePostgres 恢复原生 PostgreSQL 备份。
func RestorePostgres(ctx context.Context, dsn, source string) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return fmt.Errorf("%w: 打开 PostgreSQL 备份失败", ErrInvalidDump)
	}
	defer reader.Close()

	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[file.Name] = file
	}
	manifestFile := files["manifest.json"]
	if manifestFile == nil {
		return fmt.Errorf("%w: 缺少 manifest.json", ErrInvalidDump)
	}
	var manifest pgManifest
	if err := decodeZipJSON(manifestFile, &manifest); err != nil {
		return err
	}
	if manifest.Format != PostgresFormat {
		return fmt.Errorf("%w: 不支持的 PostgreSQL 备份格式 %q", ErrInvalidDump, manifest.Format)
	}
	var sequences pgSequences
	if err := decodeZipJSON(files["sequences.json"], &sequences); err != nil {
		return err
	}

	connect := func() (*pgx.Conn, error) {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			return nil, fmt.Errorf("解析 PostgreSQL DSN 失败: %w", err)
		}
		if cfg.RuntimeParams == nil {
			cfg.RuntimeParams = map[string]string{}
		}
		if _, ok := cfg.RuntimeParams["timezone"]; !ok {
			cfg.RuntimeParams["timezone"] = "UTC"
		}
		cfg.RuntimeParams["application_name"] = "lactssl-restore"
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
		}
		return conn, nil
	}

	// 先删除备份中出现的旧表；随后用当前程序的迁移创建完整表结构。
	conn, err := connect()
	if err != nil {
		return err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		_ = conn.Close(context.Background())
		return fmt.Errorf("开始 PostgreSQL 恢复事务失败: %w", err)
	}
	for _, table := range manifest.Tables {
		if _, err := tx.Exec(ctx, "DROP TABLE IF EXISTS "+pgx.Identifier{table.Name}.Sanitize()+" CASCADE"); err != nil {
			_ = tx.Rollback(context.Background())
			_ = conn.Close(context.Background())
			return fmt.Errorf("删除旧表 %s 失败: %w", table.Name, err)
		}
	}
	// schema_meta 不进入备份数据；必须一并删除，否则 migrate 会因版本已是最新
	// 而跳过建表，导致恢复后缺少业务表。
	if _, err := tx.Exec(ctx, "DROP TABLE IF EXISTS schema_meta CASCADE"); err != nil {
		_ = tx.Rollback(context.Background())
		_ = conn.Close(context.Background())
		return fmt.Errorf("删除 schema_meta 失败: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		_ = conn.Close(context.Background())
		return fmt.Errorf("删除旧 PostgreSQL 表失败: %w", err)
	}
	_ = conn.Close(context.Background())

	handle, err := db.OpenWithOptions(db.Options{Driver: db.Postgres, DSN: dsn, Migrate: true})
	if err != nil {
		return fmt.Errorf("创建当前 PostgreSQL 表结构失败: %w", err)
	}
	_ = handle.Close()

	conn, err = connect()
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	tx, err = conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开始 PostgreSQL 数据恢复事务失败: %w", err)
	}
	defer tx.Rollback(context.Background())

	for _, table := range manifest.Tables {
		if table.Name == "schema_meta" {
			// 版本元数据由目标程序迁移生成，避免与当前 schema_meta 冲突。
			continue
		}
		entry := files[table.Entry]
		if entry == nil {
			return fmt.Errorf("%w: 缺少表 %s 的数据", ErrInvalidDump, table.Name)
		}
		if len(table.Columns) == 0 {
			continue
		}
		data, err := entry.Open()
		if err != nil {
			return fmt.Errorf("打开表 %s 数据失败: %w", table.Name, err)
		}
		copySQL := "COPY " + pgx.Identifier{table.Name}.Sanitize() + " (" + quotedColumns(table.Columns) + ") FROM STDIN"
		_, copyErr := tx.Conn().PgConn().CopyFrom(ctx, data, copySQL)
		closeErr := data.Close()
		if copyErr != nil {
			return fmt.Errorf("恢复表 %s 数据失败: %w", table.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("关闭表 %s 数据失败: %w", table.Name, closeErr)
		}
	}

	for _, statement := range sequences.Statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("恢复 PostgreSQL 序列失败: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交 PostgreSQL 恢复事务失败: %w", err)
	}
	return nil
}

// ValidatePostgres 检测 PostgreSQL 备份格式并校验基础表。
func ValidatePostgres(filePath string) (PostgresFormatKind, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return PostgresNative, err
	}
	header := make([]byte, 4)
	_, readErr := io.ReadFull(file, header)
	_ = file.Close()
	if readErr != nil {
		return PostgresNative, ErrInvalidDump
	}
	switch string(header) {
	case "PGDM":
		// 旧版 custom 格式以 PGDMP 开头，这里只读取了 4 字节；再确认一次。
		if legacyPostgres(filePath) {
			return PostgresLegacyCustom, nil
		}
		return PostgresNative, ErrInvalidDump
	case "PK\x03\x04":
		reader, err := zip.OpenReader(filePath)
		if err != nil {
			return PostgresNative, fmt.Errorf("%w: 无法打开 PostgreSQL 备份", ErrInvalidDump)
		}
		defer reader.Close()
		var manifest pgManifest
		for _, entry := range reader.File {
			if entry.Name == "manifest.json" {
				if err := decodeZipJSON(entry, &manifest); err != nil {
					return PostgresNative, err
				}
				break
			}
		}
		if manifest.Format != PostgresFormat {
			return PostgresNative, fmt.Errorf("%w: 不支持的 PostgreSQL 备份格式", ErrInvalidDump)
		}
		names := make(map[string]bool, len(manifest.Tables))
		for _, table := range manifest.Tables {
			names[table.Name] = true
		}
		for _, required := range RequiredTables {
			if !names[required] {
				return PostgresNative, fmt.Errorf("%w: 缺少数据表 %s", ErrInvalidDump, required)
			}
		}
		return PostgresNative, nil
	default:
		return PostgresNative, ErrInvalidDump
	}
}

func legacyPostgres(filePath string) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, len(PostgresLegacyMagic))
	if _, err := io.ReadFull(file, header); err != nil {
		return false
	}
	return string(header) == PostgresLegacyMagic
}

func writeJSONEntry(zw *zip.Writer, name string, value any) error {
	entry, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return fmt.Errorf("创建备份条目 %s 失败: %w", name, err)
	}
	if err := json.NewEncoder(entry).Encode(value); err != nil {
		return fmt.Errorf("写入备份条目 %s 失败: %w", name, err)
	}
	return nil
}

func decodeZipJSON(file *zip.File, value any) error {
	if file == nil {
		return fmt.Errorf("%w: 缺少备份元数据", ErrInvalidDump)
	}
	reader, err := file.Open()
	if err != nil {
		return fmt.Errorf("%w: 读取备份元数据失败", ErrInvalidDump)
	}
	defer reader.Close()
	if err := json.NewDecoder(reader).Decode(value); err != nil {
		return fmt.Errorf("%w: 解析备份元数据失败", ErrInvalidDump)
	}
	return nil
}

func quotedColumns(columns []string) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, pgx.Identifier{column}.Sanitize())
	}
	return strings.Join(parts, ", ")
}
