package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DatabaseSettings 是配置文件中 database 段的解析结果。
type DatabaseSettings struct {
	Driver string
	Path   string
	DSN    string
}

// DatabaseFromFile 读取指定配置文件的 database 段，不生成密钥、不写入磁盘。
// 用于数据库转换回滚时定位转换前的后端。
func DatabaseFromFile(path string) (DatabaseSettings, error) {
	if _, err := os.Stat(path); err != nil {
		return DatabaseSettings{}, fmt.Errorf("读取转换前配置失败: %w", err)
	}
	fc, _, err := loadFile(path)
	if err != nil {
		return DatabaseSettings{}, err
	}
	driver := strings.ToLower(strings.TrimSpace(fc.Database.Driver))
	if driver == "" {
		driver = "sqlite"
	}
	dataDir := strings.TrimSpace(fc.DataDir)
	if dataDir == "" {
		dataDir = "data"
	}
	sqlitePath := strings.TrimSpace(fc.Database.Path)
	if sqlitePath == "" {
		sqlitePath = filepath.Join(dataDir, "lactssl.db")
	}
	dsn := strings.TrimSpace(fc.Database.DSN)
	if driver != "sqlite" && driver != "postgres" {
		return DatabaseSettings{}, fmt.Errorf("database.driver 必须是 sqlite 或 postgres")
	}
	return DatabaseSettings{Driver: driver, Path: sqlitePath, DSN: dsn}, nil
}
