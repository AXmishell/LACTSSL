package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type schemaColumn struct {
	notNull bool
	primary bool
}

type schemaSnapshot map[string]map[string]schemaColumn

func snapshotSQLiteSchema(t *testing.T, conn *Handle) schemaSnapshot {
	t.Helper()
	snapshot := schemaSnapshot{}
	for _, spec := range DataTables() {
		rows, err := conn.DB.Query(`PRAGMA table_info(` + conn.Dialect().QuoteIdent(spec.Name) + `)`)
		if err != nil {
			t.Fatal(err)
		}
		columns := map[string]schemaColumn{}
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
				_ = rows.Close()
				t.Fatal(err)
			}
			columns[name] = schemaColumn{notNull: notNull == 1 || primaryKey > 0, primary: primaryKey > 0}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		_ = rows.Close()
		snapshot[spec.Name] = columns
	}
	return snapshot
}

func snapshotInformationSchema(t *testing.T, conn *Handle) schemaSnapshot {
	t.Helper()
	snapshot := schemaSnapshot{}
	schemaExpr := "current_schema()"
	extraExpr := "''"
	if conn.Dialect().IsMySQL() {
		schemaExpr = "DATABASE()"
		extraExpr = "COALESCE(extra,'')"
	}
	rows, err := conn.DB.Query(`SELECT table_name,column_name,is_nullable,` + extraExpr + `
		FROM information_schema.columns WHERE table_schema=` + schemaExpr)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var table, column, nullable, extra string
		if err := rows.Scan(&table, &column, &nullable, &extra); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		table = strings.ToLower(table)
		if _, ok := snapshot[table]; !ok {
			snapshot[table] = map[string]schemaColumn{}
		}
		// MySQL 的生成列（CSR 在途唯一键）只存在于 MySQL 侧，不参与对齐比较。
		if strings.Contains(strings.ToUpper(extra), "GENERATED") {
			continue
		}
		snapshot[table][strings.ToLower(column)] = schemaColumn{notNull: nullable == "NO"}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	_ = rows.Close()

	pkQuery := `SELECT tc.table_name,kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON tc.constraint_name=kcu.constraint_name AND tc.table_schema=kcu.table_schema AND tc.table_name=kcu.table_name
		WHERE tc.constraint_type='PRIMARY KEY' AND tc.table_schema=` + schemaExpr
	pkRows, err := conn.DB.Query(pkQuery)
	if err != nil {
		t.Fatal(err)
	}
	for pkRows.Next() {
		var table, column string
		if err := pkRows.Scan(&table, &column); err != nil {
			_ = pkRows.Close()
			t.Fatal(err)
		}
		table, column = strings.ToLower(table), strings.ToLower(column)
		value := snapshot[table][column]
		value.primary = true
		snapshot[table][column] = value
	}
	if err := pkRows.Err(); err != nil {
		_ = pkRows.Close()
		t.Fatal(err)
	}
	return snapshot
}

func compareSchemaSnapshots(t *testing.T, want, got schemaSnapshot) {
	t.Helper()
	for table := range got {
		if table == "schema_meta" {
			continue
		}
		if _, ok := want[table]; !ok {
			t.Fatalf("多出表 %s", table)
		}
	}
	for table, wantColumns := range want {
		gotColumns, ok := got[table]
		if !ok {
			t.Fatalf("缺少表 %s", table)
		}
		for column, wantMeta := range wantColumns {
			gotMeta, ok := gotColumns[column]
			if !ok {
				t.Fatalf("表 %s 缺少列 %s", table, column)
			}
			if gotMeta.notNull != wantMeta.notNull {
				t.Fatalf("%s.%s 非空约束不一致", table, column)
			}
			if gotMeta.primary != wantMeta.primary {
				t.Fatalf("%s.%s 主键约束不一致", table, column)
			}
		}
		for column := range gotColumns {
			if _, ok := wantColumns[column]; !ok {
				t.Fatalf("表 %s 多出列 %s", table, column)
			}
		}
	}
}

// TestSchemaAlignmentAcrossDialects 对比 SQLite、PostgreSQL、MySQL 实际的表/列/非空/主键。
// PG/MySQL 需要设置对应 LACTSSL_TEST_*_DSN；CI 的 database job 会同时运行三者。
func TestSchemaAlignmentAcrossDialects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-align.db")
	sqlite, err := OpenWithOptions(Options{Driver: SQLite, Path: path, Migrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	baseline := snapshotSQLiteSchema(t, sqlite)

	check := func(driver Dialect, env string) {
		dsn := strings.TrimSpace(os.Getenv(env))
		if dsn == "" {
			t.Logf("%s 未设置，跳过 %s schema 对齐", env, driver)
			return
		}
		conn, err := OpenWithOptions(Options{Driver: driver, DSN: dsn, Migrate: true})
		if err != nil {
			t.Fatalf("打开 %s 失败: %v", driver, err)
		}
		defer conn.Close()
		compareSchemaSnapshots(t, baseline, snapshotInformationSchema(t, conn))
	}
	check(Postgres, "LACTSSL_TEST_PG_DSN")
	check(MySQL, "LACTSSL_TEST_MYSQL_DSN")
}
