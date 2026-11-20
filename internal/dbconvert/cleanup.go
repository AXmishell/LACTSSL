package dbconvert

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lactssl/internal/db"
)

const sqliteFileMagic = "SQLite format 3\x00"

// CleanupSource 在 SQLite → PostgreSQL 或 SQLite → SQLite 切换成功并通过
// 新进程自检后，删除旧的 SQLite 数据库文件及其 WAL/SHM/JOURNAL 边车文件。
// PostgreSQL → PostgreSQL 不会删除源数据库；切换前的备份目录不会被删除。
func CleanupSource(dataDir string, state *State) error {
	if state == nil ||
		state.FromDriver != db.SQLite.String() ||
		(state.ToDriver != db.Postgres.String() && state.ToDriver != db.SQLite.String()) {
		return nil
	}
	if state.ToDriver == db.SQLite.String() {
		if strings.TrimSpace(state.ToPath) == "" {
			return fmt.Errorf("目标 SQLite 路径为空，拒绝删除旧库: %s", state.FromPath)
		}
		same, err := sameSQLiteFile(state.FromPath, state.ToPath)
		if err != nil {
			return fmt.Errorf("校验旧库与目标库路径失败: %w", err)
		}
		if same {
			return fmt.Errorf("旧库与目标库是同一个 SQLite 文件，拒绝删除: %s", state.FromPath)
		}
	}
	path := strings.TrimSpace(state.FromPath)
	if path == "" {
		return nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("解析旧数据库路径失败: %w", err)
	}
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("解析数据目录失败: %w", err)
	}
	rel, err := filepath.Rel(absDataDir, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("旧数据库路径不在数据目录内，拒绝删除: %s", absPath)
	}
	if _, err := os.Stat(absPath); errors.Is(err, os.ErrNotExist) {
		// 已经删除过，保持幂等。
		return nil
	}
	if info, err := os.Stat(absPath); err == nil && info.IsDir() {
		return fmt.Errorf("旧数据库路径不是文件，拒绝删除: %s", absPath)
	}
	// 只删除确认为 SQLite 的文件，避免状态文件损坏或手工改配置时误删其它文件。
	if info, err := os.Stat(absPath); err == nil && info.Mode().IsRegular() {
		if !hasSQLiteMagic(absPath) {
			return fmt.Errorf("旧数据库文件不是 SQLite 格式，拒绝删除: %s", absPath)
		}
	}

	var errs []error
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(absPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("删除 %s 失败: %w", absPath+suffix, err))
		}
	}
	return errors.Join(errs...)
}

func hasSQLiteMagic(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, len(sqliteFileMagic))
	if _, err := file.Read(header); err != nil {
		return false
	}
	return string(header) == sqliteFileMagic
}
