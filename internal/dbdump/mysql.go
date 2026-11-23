package dbdump

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"lactssl/internal/db"
)

const (
	mysqlInsertBatchRows = 200
	mysqlInsertBatchSize = 1 << 20
	mysqlScannerSize     = 64 << 20
	mysqlMaxRestoreSize  = 256 << 20
)

type mysqlTable struct {
	Name    string
	Create  string
	Columns []mysqlColumn
}

type mysqlColumn struct {
	Name      string
	Binary    bool
	Generated bool
}

var mysqlBinaryTypes = map[string]bool{
	"binary": true, "varbinary": true,
	"tinyblob": true, "blob": true, "mediumblob": true, "longblob": true,
	"geometry": true, "point": true, "linestring": true, "polygon": true,
	"multipoint": true, "multilinestring": true, "multipolygon": true,
	"geometrycollection": true,
}

// DumpMySQL 生成 LACTSSL 原生 MySQL 逻辑备份。
func DumpMySQL(ctx context.Context, database *sql.DB, target string) error {
	if database == nil {
		return errors.New("MySQL 连接为空")
	}
	if strings.TrimSpace(target) == "" {
		return errors.New("MySQL 备份路径为空")
	}
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取 MySQL 备份连接失败: %w", err)
	}
	defer conn.Close()
	// InnoDB 一致性快照，避免备份期间数据变化导致表之间不一致。
	if _, err := conn.ExecContext(ctx, "START TRANSACTION WITH CONSISTENT SNAPSHOT"); err != nil {
		return fmt.Errorf("开始 MySQL 备份事务失败: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	}()

	tables, err := mysqlTables(ctx, conn)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("创建 MySQL 备份失败: %w", err)
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	closeWithError := func(cause error) error {
		_ = writer.Flush()
		_ = file.Close()
		return cause
	}

	if _, err := fmt.Fprintf(writer, "%s\n", MySQLHeader); err != nil {
		return closeWithError(err)
	}
	if _, err := io.WriteString(writer, "SET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS=0;\nSET UNIQUE_CHECKS=0;\n"); err != nil {
		return closeWithError(err)
	}
	for _, table := range tables {
		ident := quoteMySQLIdent(table.Name)
		if _, err := fmt.Fprintf(writer, "DROP TABLE IF EXISTS %s;\n%s\n", ident, table.Create); err != nil {
			return closeWithError(err)
		}
		columns := insertableColumns(table.Columns)
		if len(columns) == 0 {
			continue
		}
		if err := dumpMySQLTableData(ctx, writer, conn, table, columns); err != nil {
			return closeWithError(err)
		}
	}
	if _, err := io.WriteString(writer, "SET FOREIGN_KEY_CHECKS=1;\nSET UNIQUE_CHECKS=1;\n"); err != nil {
		return closeWithError(err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return closeWithError(fmt.Errorf("提交 MySQL 备份事务失败: %w", err))
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return fmt.Errorf("写入 MySQL 备份失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭 MySQL 备份失败: %w", err)
	}
	return nil
}

func mysqlTables(ctx context.Context, conn *sql.Conn) ([]mysqlTable, error) {
	rows, err := conn.QueryContext(ctx, `SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'
		ORDER BY table_name`)
	if err != nil {
		return nil, fmt.Errorf("读取 MySQL 表列表失败: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("读取 MySQL 表名失败: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取 MySQL 表列表失败: %w", err)
	}

	tables := make([]mysqlTable, 0, len(names))
	for _, name := range names {
		table := mysqlTable{Name: name}
		row := conn.QueryRowContext(ctx, "SHOW CREATE TABLE "+quoteMySQLIdent(name))
		var ignored string
		if err := row.Scan(&ignored, &table.Create); err != nil {
			return nil, fmt.Errorf("读取表 %s 结构失败: %w", name, err)
		}
		table.Create = normalizeMySQLCreate(table.Create)
		columns, err := mysqlColumns(ctx, conn, name)
		if err != nil {
			return nil, err
		}
		table.Columns = columns
		tables = append(tables, table)
	}
	return tables, nil
}

func mysqlColumns(ctx context.Context, conn *sql.Conn, table string) ([]mysqlColumn, error) {
	rows, err := conn.QueryContext(ctx, `SELECT column_name, data_type, extra
		FROM information_schema.columns
		WHERE table_schema = DATABASE() AND table_name = ?
		ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, fmt.Errorf("读取表 %s 列信息失败: %w", table, err)
	}
	defer rows.Close()

	var columns []mysqlColumn
	for rows.Next() {
		var name, dataType, extra string
		if err := rows.Scan(&name, &dataType, &extra); err != nil {
			return nil, fmt.Errorf("读取表 %s 列信息失败: %w", table, err)
		}
		columns = append(columns, mysqlColumn{
			Name:      name,
			Binary:    mysqlBinaryTypes[strings.ToLower(dataType)],
			Generated: strings.Contains(strings.ToUpper(extra), "GENERATED"),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取表 %s 列信息失败: %w", table, err)
	}
	return columns, nil
}

func dumpMySQLTableData(ctx context.Context, writer *bufio.Writer, conn *sql.Conn, table mysqlTable, columns []mysqlColumn) error {
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, quoteMySQLIdent(column.Name))
	}
	// #nosec G202 -- table/column 名称来自 information_schema，且已用反引号转义。
	query := "SELECT " + strings.Join(names, ", ") + " FROM " + quoteMySQLIdent(table.Name)
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("读取表 %s 数据失败: %w", table.Name, err)
	}
	defer rows.Close()

	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for index := range values {
		pointers[index] = &values[index]
	}

	var (
		batch      strings.Builder
		batchRows  int
		insertHead = "INSERT INTO " + quoteMySQLIdent(table.Name) + " (" + strings.Join(names, ", ") + ") VALUES "
	)
	flush := func() error {
		if batchRows == 0 {
			return nil
		}
		if _, err := writer.WriteString(batch.String()); err != nil {
			return err
		}
		if _, err := writer.WriteString(";\n"); err != nil {
			return err
		}
		batch.Reset()
		batchRows = 0
		return nil
	}

	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return fmt.Errorf("读取表 %s 数据失败: %w", table.Name, err)
		}
		if batchRows == 0 {
			batch.WriteString(insertHead)
		} else {
			batch.WriteString(", ")
		}
		batch.WriteString("(")
		for index, column := range columns {
			if index > 0 {
				batch.WriteString(", ")
			}
			literal, err := mysqlLiteral(values[index], column.Binary)
			if err != nil {
				return fmt.Errorf("格式化表 %s 数据失败: %w", table.Name, err)
			}
			batch.WriteString(literal)
		}
		batch.WriteString(")")
		batchRows++
		if batchRows >= mysqlInsertBatchRows || batch.Len() >= mysqlInsertBatchSize {
			if err := flush(); err != nil {
				return fmt.Errorf("写入表 %s 数据失败: %w", table.Name, err)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("读取表 %s 数据失败: %w", table.Name, err)
	}
	if err := flush(); err != nil {
		return fmt.Errorf("写入表 %s 数据失败: %w", table.Name, err)
	}
	return nil
}

func insertableColumns(columns []mysqlColumn) []mysqlColumn {
	out := make([]mysqlColumn, 0, len(columns))
	for _, column := range columns {
		if !column.Generated {
			out = append(out, column)
		}
	}
	return out
}

func normalizeMySQLCreate(create string) string {
	create = strings.ReplaceAll(create, "\r\n", " ")
	create = strings.ReplaceAll(create, "\n", " ")
	create = strings.ReplaceAll(create, "\t", " ")
	create = strings.TrimSpace(create)
	create = strings.TrimSuffix(create, ";")
	return create + ";"
}

func mysqlLiteral(value any, binary bool) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "NULL", nil
	case []byte:
		if binary || !utf8.Valid(typed) {
			return "X'" + hex.EncodeToString(typed) + "'", nil
		}
		return quoteMySQLString(string(typed)), nil
	case string:
		return quoteMySQLString(typed), nil
	case time.Time:
		return "'" + typed.Format("2006-01-02 15:04:05.999999") + "'", nil
	case bool:
		if typed {
			return "1", nil
		}
		return "0", nil
	case int64:
		return strconv.FormatInt(typed, 10), nil
	case int32:
		return strconv.FormatInt(int64(typed), 10), nil
	case int:
		return strconv.Itoa(typed), nil
	case uint64:
		return strconv.FormatUint(typed, 10), nil
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32), nil
	default:
		return "", fmt.Errorf("不支持的 MySQL 值类型 %T", value)
	}
}

func quoteMySQLString(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`'`, `\'`,
		`"`, `\"`,
		"\x00", `\0`,
		"\n", `\n`,
		"\r", `\r`,
		"\x1a", `\Z`,
	)
	return "'" + replacer.Replace(value) + "'"
}

func quoteMySQLIdent(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

// RestoreMySQL 恢复原生或旧版 mysqldump 生成的 SQL 备份。
func RestoreMySQL(ctx context.Context, dsn, source string) error {
	cfg, err := db.MySQLConfig(dsn)
	if err != nil {
		return err
	}
	// 恢复语句由本包拆分后逐条执行，不需要驱动层 multiStatements。
	pool, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("打开 MySQL 连接失败: %w", err)
	}
	defer pool.Close()
	// SET FOREIGN_KEY_CHECKS / SET NAMES 是连接级状态，必须固定同一条连接执行。
	conn, err := pool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取 MySQL 恢复连接失败: %w", err)
	}
	defer conn.Close()
	if err := conn.PingContext(ctx); err != nil {
		return fmt.Errorf("连接 MySQL 失败: %w", err)
	}

	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("打开 MySQL 备份失败: %w", err)
	}
	defer file.Close()

	header := make([]byte, len(MySQLHeader))
	_, readErr := io.ReadFull(file, header)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("读取 MySQL 备份失败: %w", err)
	}
	if readErr == nil && string(header) == MySQLHeader {
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), mysqlScannerSize)
		line := 0
		for scanner.Scan() {
			line++
			statement := strings.TrimSpace(scanner.Text())
			if statement == "" || strings.HasPrefix(statement, "--") || strings.HasPrefix(statement, "#") {
				continue
			}
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("恢复 MySQL 失败（第 %d 行）: %w", line, err)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("读取 MySQL 备份失败: %w", err)
		}
		return nil
	}

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("读取 MySQL 备份失败: %w", err)
	}
	if info.Size() > mysqlMaxRestoreSize {
		return ErrInvalidDump
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("读取 MySQL 备份失败: %w", err)
	}
	return restoreLegacyMySQL(ctx, conn, file)
}

// restoreLegacyMySQL 流式拆分旧版 mysqldump 的 SQL 语句，避免把整个文件
// 作为一个超大查询发送，触发 max_allowed_packet 限制。
func restoreLegacyMySQL(ctx context.Context, conn *sql.Conn, source io.Reader) error {
	reader := bufio.NewReaderSize(source, 64*1024)
	var (
		statement strings.Builder
		quote     byte
		escaped   bool
		lineNote  bool
		blockNote bool
	)
	flush := func() error {
		text := strings.TrimSpace(statement.String())
		statement.Reset()
		if text == "" {
			return nil
		}
		if strings.HasPrefix(strings.ToUpper(text), "DELIMITER ") {
			return errors.New("旧版 MySQL 备份包含 DELIMITER，请使用 mysql 客户端恢复")
		}
		if _, err := conn.ExecContext(ctx, text); err != nil {
			return fmt.Errorf("恢复 MySQL 失败: %w", err)
		}
		return nil
	}

	for {
		current, err := reader.ReadByte()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 MySQL 备份失败: %w", err)
		}

		if lineNote {
			if current == '\n' {
				lineNote = false
				statement.WriteByte('\n')
			}
			continue
		}
		if blockNote {
			statement.WriteByte(current)
			if current == '*' {
				if next, peekErr := reader.Peek(1); peekErr == nil && next[0] == '/' {
					_, _ = reader.ReadByte()
					statement.WriteByte('/')
					blockNote = false
				}
			}
			continue
		}

		if quote == 0 {
			switch {
			case current == '\'' || current == '"' || current == '`':
				quote = current
				statement.WriteByte(current)
			case current == ';':
				if err := flush(); err != nil {
					return err
				}
			case current == '#':
				lineNote = true
			case current == '-':
				next, peekErr := reader.Peek(1)
				if peekErr == nil && next[0] == '-' {
					_, _ = reader.ReadByte()
					after, afterErr := reader.Peek(1)
					if afterErr == io.EOF || (afterErr == nil && (after[0] == ' ' || after[0] == '\t' || after[0] == '\r' || after[0] == '\n')) {
						lineNote = true
						continue
					}
					statement.WriteString("--")
					continue
				}
				statement.WriteByte(current)
			case current == '/':
				next, peekErr := reader.Peek(1)
				if peekErr == nil && next[0] == '*' {
					_, _ = reader.ReadByte()
					statement.WriteString("/*")
					blockNote = true
					continue
				}
				statement.WriteByte(current)
			default:
				statement.WriteByte(current)
			}
			continue
		}

		statement.WriteByte(current)
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' && quote != '`' {
			escaped = true
			continue
		}
		if current == quote {
			if quote != '`' {
				if next, peekErr := reader.Peek(1); peekErr == nil && next[0] == quote {
					_, _ = reader.ReadByte()
					statement.WriteByte(quote)
					continue
				}
			}
			quote = 0
		}
	}
	if quote != 0 {
		return errors.New("旧版 MySQL 备份包含未闭合的字符串")
	}
	if blockNote {
		return errors.New("旧版 MySQL 备份包含未闭合的注释")
	}
	return flush()
}

// ValidateMySQL 检测 MySQL 备份格式并校验基础表。
// 返回 true 表示 LACTSSL 原生格式。
func ValidateMySQL(filePath string) (bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer file.Close()
	header := make([]byte, 512)
	n, _ := file.Read(header)
	text := string(header[:n])
	native := strings.Contains(text, MySQLHeader)

	raw, err := os.ReadFile(filePath)
	if err != nil {
		return false, err
	}
	if len(raw) == 0 {
		return false, ErrInvalidDump
	}
	full := string(raw)
	if native {
		for _, table := range RequiredTables {
			if !strings.Contains(full, "CREATE TABLE `"+table+"`") &&
				!strings.Contains(full, "CREATE TABLE "+table) {
				return true, fmt.Errorf("%w: 缺少数据表 %s", ErrInvalidDump, table)
			}
		}
		return true, nil
	}

	upper := strings.ToUpper(text)
	if !strings.Contains(upper, "MYSQL DUMP") && !strings.Contains(upper, "MARIADB DUMP") &&
		!strings.Contains(text, "/*!40101") && !strings.Contains(text, "CREATE TABLE") {
		return false, ErrInvalidDump
	}
	for _, table := range RequiredTables {
		if !strings.Contains(full, "CREATE TABLE `"+table+"`") &&
			!strings.Contains(full, "CREATE TABLE "+table) {
			return false, fmt.Errorf("%w: 缺少数据表 %s", ErrInvalidDump, table)
		}
	}
	return false, nil
}
