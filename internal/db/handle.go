package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Options 描述数据库连接参数。
type Options struct {
	Driver          Dialect
	Path            string
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ReadOnly        bool
	QueryOnly       bool
	Migrate         bool
	// SingleInstance 为 true 时，PostgreSQL 后端会持有数据库级 advisory lock，
	// 第二个实例启动会失败。默认不允许多实例共享同一个 PostgreSQL 数据库。
	SingleInstance bool
}

// Gate 是写操作门禁。转换器可以冻结写入，等待进行中的写事务结束后
// 独占写入；读操作不受影响。
type Gate struct {
	mu      sync.Mutex
	readers int
	frozen  bool
	notify  chan struct{}
}

// NewGate 创建写门禁。
func NewGate() *Gate {
	return &Gate{notify: make(chan struct{})}
}

func (g *Gate) broadcastLocked() {
	close(g.notify)
	g.notify = make(chan struct{})
}

// RLock 获取读锁；冻结期间会阻塞。
func (g *Gate) RLock() {
	g.mu.Lock()
	for g.frozen {
		ch := g.notify
		g.mu.Unlock()
		<-ch
		g.mu.Lock()
	}
	g.readers++
	g.mu.Unlock()
}

// RUnlock 释放读锁。
func (g *Gate) RUnlock() {
	g.mu.Lock()
	g.readers--
	if g.readers <= 0 {
		g.readers = 0
		g.broadcastLocked()
	}
	g.mu.Unlock()
}

// Freeze 冻结写入并等待所有已开始的写操作结束。
func (g *Gate) Freeze(ctx context.Context) error {
	g.mu.Lock()
	g.frozen = true
	for g.readers > 0 {
		ch := g.notify
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			g.mu.Lock()
			g.frozen = false
			g.broadcastLocked()
			g.mu.Unlock()
			return ctx.Err()
		case <-ch:
		}
		g.mu.Lock()
	}
	g.mu.Unlock()
	return nil
}

// Unfreeze 恢复写入。
func (g *Gate) Unfreeze() {
	g.mu.Lock()
	g.frozen = false
	g.broadcastLocked()
	g.mu.Unlock()
}

// Frozen 返回当前是否冻结。
func (g *Gate) Frozen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.frozen
}

// Handle 在 *sql.DB 外包一层方言重绑定与写门禁。
type Handle struct {
	*sql.DB
	dialect  Dialect
	gate     *Gate
	dsn      string
	lockConn *sql.Conn
}

// Close 释放单实例锁并关闭连接池。
func (h *Handle) Close() error {
	if h == nil {
		return nil
	}
	if h.lockConn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _ = h.lockConn.ExecContext(ctx, `SELECT pg_advisory_unlock($1, hashtext(current_database()))`, int32(0x4c414353))
		cancel()
		_ = h.lockConn.Close()
		h.lockConn = nil
	}
	if h.DB == nil {
		return nil
	}
	return h.DB.Close()
}

// Open 打开默认的 SQLite 数据库并执行迁移。
func Open(path string) (*Handle, error) {
	return OpenWithOptions(Options{Driver: SQLite, Path: path, Migrate: true})
}

// OpenWithOptions 按方言打开数据库。
func OpenWithOptions(opts Options) (*Handle, error) {
	driver := opts.Driver
	if driver == "" {
		driver = SQLite
	}
	switch driver {
	case SQLite:
		return openSQLite(opts)
	case Postgres:
		return openPostgres(opts)
	default:
		return nil, fmt.Errorf("不支持的数据库驱动: %s", driver)
	}
}

func openSQLite(opts Options) (*Handle, error) {
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		return nil, errors.New("SQLite 数据库路径不能为空")
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(8000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	if opts.ReadOnly {
		dsn = fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(8000)&_pragma=query_only(1)", path)
	} else if opts.QueryOnly {
		// 可写连接 + query_only：允许 SQLite 恢复/读取 WAL，但拒绝业务写入。
		dsn = fmt.Sprintf("file:%s?_pragma=busy_timeout(8000)&_pragma=journal_mode(WAL)&_pragma=query_only(1)", path)
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 写操作串行化，限制连接数可避免锁竞争。
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	if !opts.ReadOnly {
		// 限制数据库及其 WAL/SHM 权限，避免其他系统用户读取。
		for _, candidate := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
			_ = os.Chmod(candidate, 0o600)
		}
	}
	h := &Handle{DB: conn, dialect: SQLite, gate: NewGate(), dsn: dsn}
	if opts.Migrate {
		if err := migrate(h); err != nil {
			_ = h.Close()
			return nil, err
		}
	}
	return h, nil
}

func openPostgres(opts Options) (*Handle, error) {
	dsn := strings.TrimSpace(opts.DSN)
	if dsn == "" {
		return nil, errors.New("PostgreSQL DSN 不能为空")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 PostgreSQL DSN 失败: %w", err)
	}
	// 统一使用 UTC，避免 timestamptz 在不同会话时区下显示不一致。
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	if _, ok := cfg.RuntimeParams["timezone"]; !ok {
		cfg.RuntimeParams["timezone"] = "UTC"
	}
	if _, ok := cfg.RuntimeParams["application_name"]; !ok {
		cfg.RuntimeParams["application_name"] = "lactssl"
	}
	conn := stdlib.OpenDB(*cfg)
	maxOpen := opts.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 10
	}
	maxIdle := opts.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 5
	}
	conn.SetMaxOpenConns(maxOpen)
	conn.SetMaxIdleConns(maxIdle)
	if opts.ConnMaxLifetime > 0 {
		conn.SetConnMaxLifetime(opts.ConnMaxLifetime)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}
	var version int
	if err := conn.QueryRowContext(ctx, `SHOW server_version_num`).Scan(&version); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("读取 PostgreSQL 版本失败: %w", err)
	}
	if version < 160000 {
		_ = conn.Close()
		return nil, fmt.Errorf("PostgreSQL 版本过低（%d），需要 16 或以上", version)
	}
	h := &Handle{DB: conn, dialect: Postgres, gate: NewGate(), dsn: dsn}
	if opts.SingleInstance {
		lockConn, err := conn.Conn(ctx)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("获取单实例锁连接失败: %w", err)
		}
		var locked bool
		if err := lockConn.QueryRowContext(ctx,
			`SELECT pg_try_advisory_lock($1, hashtext(current_database()))`, int32(0x4c414353)).Scan(&locked); err != nil {
			_ = lockConn.Close()
			_ = conn.Close()
			return nil, fmt.Errorf("获取单实例锁失败: %w", err)
		}
		if !locked {
			_ = lockConn.Close()
			_ = conn.Close()
			return nil, errors.New("数据库已被另一个 LACTSSL 实例占用；默认不允许多实例共享 PostgreSQL")
		}
		h.lockConn = lockConn
	}
	if opts.Migrate {
		if err := migrate(h); err != nil {
			_ = h.Close()
			return nil, err
		}
	}
	return h, nil
}

// NewHandle 用已有连接构造 Handle，主要用于测试与转换目标。
func NewHandle(conn *sql.DB, dialect Dialect) *Handle {
	if dialect == "" {
		dialect = SQLite
	}
	return &Handle{DB: conn, dialect: dialect, gate: NewGate()}
}

// Dialect 返回数据库方言。
func (h *Handle) Dialect() Dialect {
	if h == nil || h.dialect == "" {
		return SQLite
	}
	return h.dialect
}

// DSN 返回打开时使用的 DSN。
func (h *Handle) DSN() string {
	if h == nil {
		return ""
	}
	return h.dsn
}

// Gate 返回写门禁。
func (h *Handle) Gate() *Gate {
	if h == nil {
		return nil
	}
	return h.gate
}

// Raw 返回底层 *sql.DB。
func (h *Handle) Raw() *sql.DB {
	if h == nil {
		return nil
	}
	return h.DB
}

// Freeze 冻结写入。
func (h *Handle) Freeze(ctx context.Context) error {
	if h == nil || h.gate == nil {
		return nil
	}
	return h.gate.Freeze(ctx)
}

// Unfreeze 恢复写入。
func (h *Handle) Unfreeze() {
	if h != nil && h.gate != nil {
		h.gate.Unfreeze()
	}
}

func (h *Handle) gatedExec(query string) bool {
	return h != nil && h.gate != nil && isWriteSQL(query)
}

// Exec 执行语句，写语句受门禁保护。
func (h *Handle) Exec(query string, args ...any) (sql.Result, error) {
	if h.gatedExec(query) {
		h.gate.RLock()
		defer h.gate.RUnlock()
	}
	return h.DB.Exec(h.Dialect().Rebind(query), args...)
}

// ExecContext 执行语句，写语句受门禁保护。
func (h *Handle) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if h.gatedExec(query) {
		h.gate.RLock()
		defer h.gate.RUnlock()
	}
	return h.DB.ExecContext(ctx, h.Dialect().Rebind(query), args...)
}

// Query 执行查询。
func (h *Handle) Query(query string, args ...any) (*sql.Rows, error) {
	if h.gatedExec(query) {
		h.gate.RLock()
		defer h.gate.RUnlock()
	}
	return h.DB.Query(h.Dialect().Rebind(query), args...)
}

// QueryContext 执行查询。
func (h *Handle) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if h.gatedExec(query) {
		h.gate.RLock()
		defer h.gate.RUnlock()
	}
	return h.DB.QueryContext(ctx, h.Dialect().Rebind(query), args...)
}

// Row 包装 *sql.Row，并在 Scan 后释放写门禁。
type Row struct {
	row    *sql.Row
	unlock func()
}

// Scan 读取单行结果。
func (r *Row) Scan(dest ...any) error {
	if r.unlock != nil {
		defer r.unlock()
	}
	return r.row.Scan(dest...)
}

// QueryRow 执行单行查询。写入型 RETURNING 语句在 Scan 完成后释放门禁。
func (h *Handle) QueryRow(query string, args ...any) *Row {
	row := &Row{}
	if h.gatedExec(query) {
		h.gate.RLock()
		row.unlock = h.gate.RUnlock
	}
	row.row = h.DB.QueryRow(h.Dialect().Rebind(query), args...)
	return row
}

// QueryRowContext 执行单行查询。
func (h *Handle) QueryRowContext(ctx context.Context, query string, args ...any) *Row {
	row := &Row{}
	if h.gatedExec(query) {
		h.gate.RLock()
		row.unlock = h.gate.RUnlock
	}
	row.row = h.DB.QueryRowContext(ctx, h.Dialect().Rebind(query), args...)
	return row
}

// Begin 开启事务并持有写门禁，直到 Commit/Rollback。
func (h *Handle) Begin() (*Tx, error) {
	if h.gate != nil {
		h.gate.RLock()
	}
	tx, err := h.DB.Begin()
	if err != nil {
		if h.gate != nil {
			h.gate.RUnlock()
		}
		return nil, err
	}
	return &Tx{Tx: tx, dialect: h.Dialect(), gate: h.gate}, nil
}

// BeginTx 开启事务并持有写门禁。
func (h *Handle) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	if h.gate != nil {
		h.gate.RLock()
	}
	tx, err := h.DB.BeginTx(ctx, opts)
	if err != nil {
		if h.gate != nil {
			h.gate.RUnlock()
		}
		return nil, err
	}
	return &Tx{Tx: tx, dialect: h.Dialect(), gate: h.gate}, nil
}

// InsertID 执行 INSERT 并返回自增 id。
// PostgreSQL 使用 RETURNING id；SQLite 使用 LastInsertId。
func (h *Handle) InsertID(query string, args ...any) (int64, error) {
	if h.Dialect() == Postgres {
		var id int64
		err := h.QueryRow(strings.TrimSpace(query)+` RETURNING id`, args...).Scan(&id)
		return id, err
	}
	h.gate.RLock()
	defer h.gate.RUnlock()
	res, err := h.DB.Exec(h.Dialect().Rebind(query), args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Tx 是带方言重绑定的事务。提交或回滚时释放写门禁。
type Tx struct {
	*sql.Tx
	dialect Dialect
	gate    *Gate
	once    sync.Once
}

func (tx *Tx) release() {
	if tx.gate == nil {
		return
	}
	tx.once.Do(func() { tx.gate.RUnlock() })
}

// Exec 执行语句。
func (tx *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.Tx.Exec(tx.dialect.Rebind(query), args...)
}

// ExecContext 执行语句。
func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.Tx.ExecContext(ctx, tx.dialect.Rebind(query), args...)
}

// Query 执行查询。
func (tx *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.Query(tx.dialect.Rebind(query), args...)
}

// QueryContext 执行查询。
func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.QueryContext(ctx, tx.dialect.Rebind(query), args...)
}

// QueryRow 执行单行查询。
func (tx *Tx) QueryRow(query string, args ...any) *sql.Row {
	return tx.Tx.QueryRow(tx.dialect.Rebind(query), args...)
}

// QueryRowContext 执行单行查询。
func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.Tx.QueryRowContext(ctx, tx.dialect.Rebind(query), args...)
}

// InsertID 执行 INSERT 并返回自增 id。
func (tx *Tx) InsertID(query string, args ...any) (int64, error) {
	if tx.dialect == Postgres {
		var id int64
		err := tx.QueryRow(strings.TrimSpace(query)+` RETURNING id`, args...).Scan(&id)
		return id, err
	}
	res, err := tx.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Commit 提交事务并释放门禁。
func (tx *Tx) Commit() error {
	err := tx.Tx.Commit()
	tx.release()
	return err
}

// Rollback 回滚事务并释放门禁。
func (tx *Tx) Rollback() error {
	err := tx.Tx.Rollback()
	tx.release()
	return err
}
