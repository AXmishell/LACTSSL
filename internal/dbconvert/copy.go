package dbconvert

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"

	"lactssl/internal/db"
)

// cell 是跨方言的通用值。database/sql 驱动把原始值交给 Scan，
// 这里统一转成目标驱动可接受的 Go 类型。
type cell struct {
	value any
	valid bool
}

func (c *cell) Scan(src any) error {
	c.valid = src != nil
	if src == nil {
		c.value = nil
		return nil
	}
	switch v := src.(type) {
	case []byte:
		c.value = string(v)
	case time.Time:
		c.value = v.UTC()
	default:
		c.value = v
	}
	return nil
}

func (c cell) arg() any {
	if !c.valid {
		return nil
	}
	return c.value
}

// canonical 生成稳定的行编码，用于跨数据库校验。
func (c cell) canonical(buf *strings.Builder) {
	if !c.valid {
		buf.WriteString("N;")
		return
	}
	switch v := c.value.(type) {
	case int64:
		fmt.Fprintf(buf, "I%d;", v)
	case int32:
		fmt.Fprintf(buf, "I%d;", v)
	case int:
		fmt.Fprintf(buf, "I%d;", v)
	case float64:
		fmt.Fprintf(buf, "F%x;", math.Float64bits(v))
	case bool:
		if v {
			buf.WriteString("B1;")
		} else {
			buf.WriteString("B0;")
		}
	case time.Time:
		// PostgreSQL timestamptz 只有微秒精度，SQLite 文本可能保留纳秒；
		// 校验时统一截断到微秒，避免无意义的精度差异。
		value := v.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z07:00")
		fmt.Fprintf(buf, "T%s;", value)
	default:
		text := fmt.Sprint(v)
		fmt.Fprintf(buf, "S%d:%s;", len(text), text)
	}
}

type hashAccumulator struct {
	xor [sha256.Size]byte
}

func (h *hashAccumulator) addRow(cells []cell) {
	var buf strings.Builder
	for _, c := range cells {
		c.canonical(&buf)
	}
	sum := sha256.Sum256([]byte(buf.String()))
	for i := range h.xor {
		h.xor[i] ^= sum[i]
	}
}

func (h *hashAccumulator) String() string {
	return hex.EncodeToString(h.xor[:])
}

// tableSelection 返回复制查询。主键用于稳定读取顺序。
func tableSelection(spec db.TableSpec) string {
	query := `SELECT * FROM ` + quoteIdent(spec.Name)
	if len(spec.PrimaryKey) > 0 {
		parts := make([]string, 0, len(spec.PrimaryKey))
		for _, key := range spec.PrimaryKey {
			parts = append(parts, quoteIdent(key))
		}
		query += ` ORDER BY ` + strings.Join(parts, ",")
	}
	return query
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// copyTable 把一张表从 source 复制到 target，并返回行数、字节估算与校验和。
func copyTable(ctx context.Context, source, target *db.Handle, spec db.TableSpec, onRows func(int64)) (TableProgress, error) {
	result := TableProgress{Name: spec.Name, Status: "copying"}
	rows, err := source.QueryContext(ctx, tableSelection(spec))
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result, fmt.Errorf("读取表 %s 失败: %w", spec.Name, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return result, err
	}
	if len(columns) == 0 {
		result.Status = "done"
		return result, nil
	}
	insertSQL := `INSERT INTO ` + quoteIdent(spec.Name) + `(` + joinIdentifiers(columns) + `) VALUES `
	const maxParams = 900 // SQLite 保守上限；PG 更高，统一按小批量执行
	batchSize := maxParams / len(columns)
	if batchSize < 1 {
		batchSize = 1
	}
	if batchSize > 500 {
		batchSize = 500
	}
	tx, err := target.Begin()
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		batchArgs []any
		batchRows int
		hash      hashAccumulator
		total     int64
		bytes     int64
	)
	flush := func() error {
		if batchRows == 0 {
			return nil
		}
		placeholders := strings.TrimSuffix(strings.Repeat("("+strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")+"),", batchRows), ",")
		if _, err := tx.Exec(insertSQL+placeholders, batchArgs...); err != nil {
			return err
		}
		batchArgs = batchArgs[:0]
		batchRows = 0
		return nil
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		values := make([]cell, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return result, fmt.Errorf("读取表 %s 数据失败: %w", spec.Name, err)
		}
		for _, value := range values {
			arg := value.arg()
			batchArgs = append(batchArgs, arg)
			if text, ok := arg.(string); ok {
				bytes += int64(len(text))
			}
		}
		hash.addRow(values)
		total++
		batchRows++
		if batchRows >= batchSize {
			if err := flush(); err != nil {
				return result, fmt.Errorf("写入表 %s 失败: %w", spec.Name, err)
			}
			if onRows != nil {
				onRows(total)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if err := flush(); err != nil {
		return result, fmt.Errorf("写入表 %s 失败: %w", spec.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("提交表 %s 失败: %w", spec.Name, err)
	}
	result.Rows = total
	result.Bytes = bytes
	result.Hash = hash.String()
	result.Status = "done"
	if onRows != nil {
		onRows(total)
	}
	return result, nil
}

func joinIdentifiers(columns []string) string {
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		out = append(out, quoteIdent(column))
	}
	return strings.Join(out, ",")
}

// scanTable 只读取表并计算行数与校验和，用于目标库复核。
func scanTable(ctx context.Context, conn *db.Handle, spec db.TableSpec) (TableProgress, error) {
	result := TableProgress{Name: spec.Name, Status: "verifying"}
	rows, err := conn.QueryContext(ctx, tableSelection(spec))
	if err != nil {
		return result, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return result, err
	}
	var (
		hash  hashAccumulator
		total int64
		bytes int64
	)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		values := make([]cell, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return result, err
		}
		hash.addRow(values)
		total++
		for _, value := range values {
			if text, ok := value.arg().(string); ok {
				bytes += int64(len(text))
			}
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	result.Rows = total
	result.Bytes = bytes
	result.Hash = hash.String()
	result.Status = "verified"
	return result, nil
}

func countTable(ctx context.Context, conn *db.Handle, table string) (int64, error) {
	var count int64
	err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(table)).Scan(&count)
	return count, err
}

// resetSequences 把 PostgreSQL identity 序列推进到当前最大 ID。
func resetSequences(ctx context.Context, target *db.Handle, tables []db.TableSpec) error {
	if !target.Dialect().IsPostgres() {
		return nil
	}
	for _, spec := range tables {
		if !spec.HasID {
			continue
		}
		query := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s','id'), COALESCE(MAX(id),1), MAX(id) IS NOT NULL) FROM %s`,
			strings.ReplaceAll(spec.Name, "'", "''"), quoteIdent(spec.Name))
		if _, err := target.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("重置 %s 序列失败: %w", spec.Name, err)
		}
	}
	return nil
}

// tableExists 判断源/目标表是否存在。
func tableExists(ctx context.Context, conn *db.Handle, table string) (bool, error) {
	if conn.Dialect().IsPostgres() {
		var exists bool
		err := conn.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema=current_schema() AND table_name=?
		)`, table).Scan(&exists)
		return exists, err
	}
	var name string
	err := conn.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}
