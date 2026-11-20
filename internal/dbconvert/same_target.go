package dbconvert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lactssl/internal/db"
)

// sameSQLiteFile 判断两个路径是否指向同一个物理文件，覆盖相对路径、
// 软链接和硬链接。目标文件不存在时只比较绝对路径。
func sameSQLiteFile(sourcePath, targetPath string) (bool, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	targetPath = strings.TrimSpace(targetPath)
	if sourcePath == "" || targetPath == "" {
		return false, nil
	}
	sourceAbs, err := filepath.Abs(sourcePath)
	if err != nil {
		return false, fmt.Errorf("解析源 SQLite 路径失败: %w", err)
	}
	targetAbs, err := filepath.Abs(targetPath)
	if err != nil {
		return false, fmt.Errorf("解析目标 SQLite 路径失败: %w", err)
	}
	if filepath.Clean(sourceAbs) == filepath.Clean(targetAbs) {
		return true, nil
	}
	sourceInfo, err := os.Stat(sourceAbs)
	if err != nil {
		return false, fmt.Errorf("读取源 SQLite 文件失败: %w", err)
	}
	targetInfo, err := os.Stat(targetAbs)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取目标 SQLite 文件失败: %w", err)
	}
	return os.SameFile(sourceInfo, targetInfo), nil
}

// postgresIdentity 返回能标识“同一 PostgreSQL 实例 + 同一数据库”的字符串。
// 不比较 host：同一实例可能通过不同网卡地址或 DNS 别名访问。
// 同一数据库的不同 schema 也视为同一目标，因为应用的单实例锁按数据库生效。
func postgresIdentity(ctx context.Context, conn *db.Handle) (string, error) {
	if conn == nil {
		return "", errors.New("PostgreSQL 连接为空")
	}
	var database string
	var started time.Time
	if err := conn.QueryRowContext(ctx, `SELECT current_database(), pg_postmaster_start_time()`).Scan(&database, &started); err != nil {
		return "", err
	}
	return database + "|" + started.UTC().Format(time.RFC3339Nano), nil
}

// ensureDistinctTarget 拒绝把源数据库本身当作同构迁移目标。
// 跨类型切换不做额外限制；同一目标检测只在 sourceDriver == targetDriver 时执行。
func ensureDistinctTarget(ctx context.Context, source, target *db.Handle, sourceDriver, targetDriver db.Dialect, sourcePath, targetPath string) error {
	if sourceDriver != targetDriver {
		return nil
	}
	switch sourceDriver {
	case db.SQLite:
		same, err := sameSQLiteFile(sourcePath, targetPath)
		if err != nil {
			return err
		}
		if same {
			return errors.New("目标 SQLite 文件不能与当前数据库是同一个文件")
		}
	case db.Postgres:
		if source == nil || target == nil {
			return errors.New("无法校验 PostgreSQL 源/目标连接")
		}
		sourceID, err := postgresIdentity(ctx, source)
		if err != nil {
			return fmt.Errorf("读取源 PostgreSQL 标识失败: %w", err)
		}
		targetID, err := postgresIdentity(ctx, target)
		if err != nil {
			return fmt.Errorf("读取目标 PostgreSQL 标识失败: %w", err)
		}
		if sourceID == targetID {
			return errors.New("目标 PostgreSQL 与当前数据库是同一实例和数据库，不能作为同构迁移目标")
		}
	}
	return nil
}
