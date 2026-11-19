package logarchive

import "time"

// Manifest 是归档文件的清单，同时作为归档目录扫描的权威元数据。
type Manifest struct {
	Schema     int       `json:"schema"`
	ArchiveID  string    `json:"archive_id"`
	Category   string    `json:"category"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
	AppVersion string    `json:"app_version,omitempty"`
	Operator   Operator  `json:"operator"`
	SourceDB   string    `json:"source_db,omitempty"`

	RowCount       int64          `json:"row_count"`
	FirstLogID     int64          `json:"first_log_id"`
	LastLogID      int64          `json:"last_log_id"`
	FirstCreatedAt *time.Time     `json:"first_created_at,omitempty"`
	LastCreatedAt  *time.Time     `json:"last_created_at,omitempty"`
	Filters        map[string]any `json:"filters,omitempty"`
	File           FileInfo       `json:"file"`
}

// Operator 记录触发归档的管理员；自动清理时为空。
type Operator struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email,omitempty"`
}

// FileInfo 描述数据文件本身。
type FileInfo struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Entry 是索引中的归档项，附带磁盘路径与文件缺失状态。
type Entry struct {
	Manifest
	ManifestPath string `json:"-"`
	DataPath     string `json:"-"`
	Missing      bool   `json:"missing"`
}
