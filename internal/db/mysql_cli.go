package db

import (
	"os/exec"
	"strings"
)

// MySQLDumpArgs 生成 mysqldump/mariadb-dump 的参数。
// 密码统一通过 MYSQL_PWD 传入，不进入进程参数。
func MySQLDumpArgs(binary string, connection MySQLCLIConnection, target string) []string {
	args := []string{
		"--single-transaction",
		"--skip-lock-tables",
		"--no-tablespaces",
		"--skip-routines",
		"--skip-triggers",
		"--skip-events",
		"--default-character-set=utf8mb4",
		"--result-file=" + target,
	}
	args = append(args, mysqlConnectionArgs(binary, connection)...)
	if connection.Database != "" {
		args = append(args, connection.Database)
	}
	return args
}

// MySQLRestoreArgs 生成 mysql/mariadb 客户端恢复参数。
func MySQLRestoreArgs(binary string, connection MySQLCLIConnection) []string {
	args := []string{"--batch", "--default-character-set=utf8mb4"}
	args = append(args, mysqlConnectionArgs(binary, connection)...)
	if connection.Database != "" {
		args = append(args, "--database="+connection.Database)
	}
	return args
}

func mysqlConnectionArgs(binary string, connection MySQLCLIConnection) []string {
	args := make([]string, 0, 8)
	if connection.Socket != "" {
		args = append(args, "--socket="+connection.Socket)
	} else {
		args = append(args, "--host="+connection.Host, "--port="+connection.Port, "--protocol=TCP")
		args = append(args, mysqlTLSArgs(binary, connection.TLSMode)...)
	}
	if connection.User != "" {
		args = append(args, "--user="+connection.User)
	}
	return args
}

// mysqlTLSArgs 兼容 MySQL 与 MariaDB 客户端的不同 TLS 选项。
func mysqlTLSArgs(binary, tlsMode string) []string {
	if isMariaDBClient(binary) {
		switch strings.ToUpper(strings.TrimSpace(tlsMode)) {
		case "", "DISABLED":
			return []string{"--skip-ssl"}
		case "VERIFY_IDENTITY":
			return []string{"--ssl", "--ssl-verify-server-cert"}
		default:
			return []string{"--ssl"}
		}
	}
	if tlsMode == "" {
		return nil
	}
	return []string{"--ssl-mode=" + tlsMode}
}

// isMariaDBClient 通过客户端版本输出判断是否为 MariaDB 客户端。
func isMariaDBClient(binary string) bool {
	output, err := exec.Command(binary, "--version").CombinedOutput() // #nosec G204 -- binary 由 findMySQLDump/findMySQLClient 从 PATH 解析
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(output)), "mariadb")
}
