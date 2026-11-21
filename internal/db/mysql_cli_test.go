package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMySQLDumpArgsMariaDB(t *testing.T) {
	binary := fakeMySQLBinary(t, "mysqldump  from 11.8.10-MariaDB, client 10.20 for Linux")
	connection := MySQLCLIConnection{Host: "127.0.0.1", Port: "3306", User: "root", Database: "lactssl", TLSMode: "DISABLED"}
	args := strings.Join(MySQLDumpArgs(binary, connection, "/tmp/backup.sql"), " ")
	if !strings.Contains(args, "--skip-ssl") || strings.Contains(args, "--ssl-mode") {
		t.Fatalf("MariaDB 客户端 TLS 参数异常: %s", args)
	}
	if !strings.Contains(args, "--host=127.0.0.1 --port=3306 --protocol=TCP") || !strings.Contains(args, "--user=root") {
		t.Fatalf("MariaDB 连接参数异常: %s", args)
	}
}

func TestMySQLDumpArgsMySQL(t *testing.T) {
	binary := fakeMySQLBinary(t, "mysqldump  Ver 8.0.36 for Linux on x86_64 (MySQL Community Server - GPL)")
	connection := MySQLCLIConnection{Host: "db", Port: "3307", User: "app", Database: "lactssl", TLSMode: "VERIFY_IDENTITY"}
	args := strings.Join(MySQLDumpArgs(binary, connection, "/tmp/backup.sql"), " ")
	if !strings.Contains(args, "--ssl-mode=VERIFY_IDENTITY") || strings.Contains(args, "--skip-ssl") {
		t.Fatalf("MySQL 客户端 TLS 参数异常: %s", args)
	}
}

func fakeMySQLBinary(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mysql-client")
	script := "#!/bin/sh\necho '" + version + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
