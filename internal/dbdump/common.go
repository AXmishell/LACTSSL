// Package dbdump 提供不依赖外部 pg_dump/mysqldump 的数据库逻辑备份与恢复。
//
// PostgreSQL 使用原生 ZIP 容器（manifest/schema/sequences/tables），数据通过
// COPY 文本协议导出和导入；MySQL/MariaDB 使用带固定头部的 SQL 脚本，恢复时
// 通过 go-sql-driver/mysql 逐条执行。旧版 PostgreSQL custom 格式仍可检测，
// 但需要外部 pg_restore 才能恢复。
package dbdump

import (
	"errors"
	"strings"
)

const (
	// PostgresFormat 是原生 PostgreSQL 备份容器的格式标识。
	PostgresFormat = "lactssl-pgcopy-v1"
	// MySQLHeader 是原生 MySQL 逻辑备份的第一行。
	MySQLHeader = "-- LACTSSL-NATIVE-MYSQL-V1"
	// PostgresLegacyMagic 是旧版 pg_dump custom 格式头。
	PostgresLegacyMagic = "PGDMP"
)

// RequiredTables 是恢复时要求存在的基础表。
var RequiredTables = []string{
	"users",
	"settings",
	"dns_accounts",
	"deploy_accounts",
	"private_cas",
	"certificates",
	"tasks",
	"deploy_records",
	"monitors",
	"logs",
}

// PostgresFormatKind 描述 PostgreSQL 备份文件格式。
type PostgresFormatKind int

const (
	// PostgresNative 是 LACTSSL 原生 go dump 格式。
	PostgresNative PostgresFormatKind = iota
	// PostgresLegacyCustom 是旧版 pg_dump --format=custom 格式。
	PostgresLegacyCustom
)

// ErrInvalidDump 表示备份文件格式不正确。
var ErrInvalidDump = errors.New("数据库备份格式不正确")

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
