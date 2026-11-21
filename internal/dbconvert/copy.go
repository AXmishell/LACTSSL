package dbconvert

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
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
		// MySQL DATETIME(6) 与 PostgreSQL timestamptz 只有微秒精度；
		// 写入前统一截断，避免 MySQL 四舍五入导致校验和差异。
		c.value = v.UTC().Truncate(time.Microsecond)
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

// addRow 按列名排序后计算行哈希。不同方言的 SELECT * 列顺序可能不同
// （例如 SQLite 用 ALTER TABLE 追加的列会排在最后），按列名归一化才能跨库比较。
func (h *hashAccumulator) addRow(cells []cell, order []int) {
	var buf strings.Builder
	for _, index := range order {
		cells[index].canonical(&buf)
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
func tableSelection(dialect db.Dialect, spec db.TableSpec) string {
	query := `SELECT * FROM ` + quoteIdent(dialect, spec.Name)
	if len(spec.PrimaryKey) > 0 {
		parts := make([]string, 0, len(spec.PrimaryKey))
		for _, key := range spec.PrimaryKey {
			parts = append(parts, quoteIdent(dialect, key))
		}
		query += ` ORDER BY ` + strings.Join(parts, ",")
	}
	return query
}

func quoteIdent(dialect db.Dialect, name string) string {
	return dialect.QuoteIdent(name)
}

// copyTable 把一张表从 source 复制到 target，并返回行数、字节估算与校验和。
func copyTable(ctx context.Context, source, target *db.Handle, spec db.TableSpec, onRows func(int64)) (TableProgress, error) {
	result := TableProgress{Name: spec.Name, Status: "copying"}
	rows, err := source.QueryContext(ctx, tableSelection(source.Dialect(), spec))
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
	targetColumns, err := tableColumns(ctx, target, spec.Name)
	if err != nil {
		return result, err
	}
	// 只比较两端都存在的列：MySQL 生成列（例如 CSR 在途申请唯一键）
	// 可能只存在于源或目标，不能参与跨方言哈希，也不能显式写入。
	hashIndexes := make([]int, 0, len(columns))
	insertIndexes := make([]int, 0, len(columns))
	for index, name := range columns {
		if !targetColumns[strings.ToLower(name)] {
			continue
		}
		hashIndexes = append(hashIndexes, index)
		insertIndexes = append(insertIndexes, index)
	}
	if target.Dialect().IsMySQL() {
		generated, err := generatedColumns(ctx, target, spec.Name)
		if err != nil {
			return result, err
		}
		if len(generated) > 0 {
			filtered := make([]int, 0, len(insertIndexes))
			for _, index := range insertIndexes {
				if !generated[strings.ToLower(columns[index])] {
					filtered = append(filtered, index)
				}
			}
			insertIndexes = filtered
		}
	}
	order := columnOrderFor(columns, hashIndexes)
	selected := make([]string, 0, len(hashIndexes))
	for _, index := range hashIndexes {
		selected = append(selected, columns[index])
	}
	result.columns = selected
	insertColumns := make([]string, 0, len(insertIndexes))
	for _, index := range insertIndexes {
		insertColumns = append(insertColumns, columns[index])
	}
	insertSQL := `INSERT INTO ` + quoteIdent(target.Dialect(), spec.Name) + `(` + joinIdentifiers(target.Dialect(), insertColumns) + `) VALUES `
	const maxParams = 900 // SQLite 保守上限；PG/MySQL 更高，统一按小批量执行
	batchSize := maxParams / len(insertColumns)
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
		placeholders := strings.TrimSuffix(strings.Repeat("("+strings.TrimSuffix(strings.Repeat("?,", len(insertColumns)), ",")+"),", batchRows), ",")
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
		for _, index := range insertIndexes {
			batchArgs = append(batchArgs, values[index].arg())
		}
		for _, value := range values {
			if text, ok := value.arg().(string); ok {
				bytes += int64(len(text))
			}
		}
		hash.addRow(values, order)
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

func joinIdentifiers(dialect db.Dialect, columns []string) string {
	out := make([]string, 0, len(columns))
	for _, column := range columns {
		out = append(out, quoteIdent(dialect, column))
	}
	return strings.Join(out, ",")
}

// scanTable 只读取表并计算行数与校验和，用于目标库复核。
// restrict 非空时只对指定列计算哈希，用于目标库存在源库没有的生成列的跨方言场景。
func scanTable(ctx context.Context, conn *db.Handle, spec db.TableSpec, restrict []string) (TableProgress, error) {
	result := TableProgress{Name: spec.Name, Status: "verifying"}
	rows, err := conn.QueryContext(ctx, tableSelection(conn.Dialect(), spec))
	if err != nil {
		return result, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return result, err
	}
	indexes := make([]int, 0, len(columns))
	if len(restrict) == 0 {
		for index := range columns {
			indexes = append(indexes, index)
		}
	} else {
		allowed := make(map[string]bool, len(restrict))
		for _, name := range restrict {
			allowed[strings.ToLower(name)] = true
		}
		for index, name := range columns {
			if allowed[strings.ToLower(name)] {
				indexes = append(indexes, index)
			}
		}
	}
	order := columnOrderFor(columns, indexes)
	selected := make([]string, 0, len(indexes))
	for _, index := range indexes {
		selected = append(selected, columns[index])
	}
	result.columns = selected
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
		hash.addRow(values, order)
		total++
		for _, index := range indexes {
			if text, ok := values[index].arg().(string); ok {
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

// columnOrder 返回按列名升序排列的下标，用于稳定行编码。
func columnOrder(columns []string) []int {
	order := make([]int, len(columns))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(a, b int) bool {
		return columns[order[a]] < columns[order[b]]
	})
	return order
}

// columnOrderFor 对指定列下标按列名排序，保证跨方言哈希一致。
func columnOrderFor(columns []string, indexes []int) []int {
	order := append([]int(nil), indexes...)
	sort.Slice(order, func(a, b int) bool {
		return columns[order[a]] < columns[order[b]]
	})
	return order
}

func countTable(ctx context.Context, conn *db.Handle, table string) (int64, error) {
	var count int64
	err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(conn.Dialect(), table)).Scan(&count)
	return count, err
}

// resetSequences 把 PostgreSQL identity 序列或 MySQL AUTO_INCREMENT 推进到当前最大 ID。
func resetSequences(ctx context.Context, target *db.Handle, tables []db.TableSpec) error {
	switch {
	case target.Dialect().IsPostgres():
		for _, spec := range tables {
			if !spec.HasID {
				continue
			}
			query := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s','id'), COALESCE(MAX(id),1), MAX(id) IS NOT NULL) FROM %s`,
				strings.ReplaceAll(spec.Name, "'", "''"), quoteIdent(target.Dialect(), spec.Name))
			if _, err := target.ExecContext(ctx, query); err != nil {
				return fmt.Errorf("重置 %s 序列失败: %w", spec.Name, err)
			}
		}
	case target.Dialect().IsMySQL():
		for _, spec := range tables {
			if !spec.HasID {
				continue
			}
			var maxID int64
			if err := target.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM `+quoteIdent(target.Dialect(), spec.Name)).Scan(&maxID); err != nil {
				return fmt.Errorf("读取 %s 最大 ID 失败: %w", spec.Name, err)
			}
			query := fmt.Sprintf(`ALTER TABLE %s AUTO_INCREMENT = %d`, quoteIdent(target.Dialect(), spec.Name), maxID+1)
			if _, err := target.ExecContext(ctx, query); err != nil {
				return fmt.Errorf("重置 %s 自增序列失败: %w", spec.Name, err)
			}
		}
	}
	return nil
}

// tableExists 判断源/目标表是否存在。
func tableExists(ctx context.Context, conn *db.Handle, table string) (bool, error) {
	if conn.Dialect().IsMySQL() {
		var count int64
		err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema=DATABASE() AND table_name=?`, table).Scan(&count)
		return count > 0, err
	}
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

// tableColumns 返回目标表的列名（小写），用于跨方言复制时求交集。
func tableColumns(ctx context.Context, conn *db.Handle, table string) (map[string]bool, error) {
	result := map[string]bool{}
	switch {
	case conn.Dialect().IsMySQL():
		rows, err := conn.QueryContext(ctx, `SELECT LOWER(column_name) FROM information_schema.columns
			WHERE table_schema=DATABASE() AND table_name=?`, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, err
			}
			result[name] = true
		}
		return result, rows.Err()
	case conn.Dialect().IsPostgres():
		rows, err := conn.QueryContext(ctx, `SELECT LOWER(column_name) FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name=?`, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, err
			}
			result[name] = true
		}
		return result, rows.Err()
	default:
		rows, err := conn.QueryContext(ctx, `PRAGMA table_info(`+quoteIdent(conn.Dialect(), table)+`)`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				cid        int
				name       string
				columnType string
				notNull    int
				defaultVal any
				primaryKey int
			)
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &primaryKey); err != nil {
				return nil, err
			}
			result[strings.ToLower(name)] = true
		}
		return result, rows.Err()
	}
}

// generatedColumns 返回 MySQL 表中的生成列名（小写）。
func generatedColumns(ctx context.Context, conn *db.Handle, table string) (map[string]bool, error) {
	if !conn.Dialect().IsMySQL() {
		return nil, nil
	}
	rows, err := conn.QueryContext(ctx, `SELECT LOWER(column_name) FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name=? AND extra LIKE '%GENERATED%'`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}
