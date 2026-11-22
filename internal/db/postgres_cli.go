package db

import (
	"net/url"
	"strings"
)

// PostgresCLIEnv 把 PostgreSQL DSN 中的密码移到 PGPASSWORD 环境变量，
// 返回移除密码后的 DSN 与需要追加到子进程的环境变量。
//
// pg_dump/pg_restore 会把密码作为 argv 传给子进程时，同机其他用户可通过
// /proc/<pid>/cmdline 读取；本函数保证密码只出现在环境变量中。
func PostgresCLIEnv(dsn string) (string, []string) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return dsn, nil
	}
	if strings.Contains(dsn, "://") {
		parsed, err := url.Parse(dsn)
		if err != nil || parsed.Scheme == "" {
			return dsn, nil
		}
		if parsed.User == nil {
			return dsn, nil
		}
		password, hasPassword := parsed.User.Password()
		if !hasPassword || password == "" {
			return dsn, nil
		}
		parsed.User = url.User(parsed.User.Username())
		return parsed.String(), []string{"PGPASSWORD=" + password}
	}
	safe, password := stripPostgresKeywordPassword(dsn)
	if password == "" {
		return dsn, nil
	}
	return safe, []string{"PGPASSWORD=" + password}
}

// stripPostgresKeywordPassword 从 libpq 关键字式 DSN 中移除 password=...，
// 保留其他键值与原始引号/转义。返回移除后的 DSN 和密码。
func stripPostgresKeywordPassword(dsn string) (string, string) {
	var out strings.Builder
	password := ""
	i := 0
	for i < len(dsn) {
		start := i
		for i < len(dsn) && isPostgresDSNSpace(dsn[i]) {
			i++
		}
		out.WriteString(dsn[start:i])
		if i >= len(dsn) {
			break
		}
		keyStart := i
		for i < len(dsn) && dsn[i] != '=' && !isPostgresDSNSpace(dsn[i]) {
			i++
		}
		key := dsn[keyStart:i]
		if i >= len(dsn) || dsn[i] != '=' {
			out.WriteString(dsn[keyStart:i])
			continue
		}
		i++ // '='
		for i < len(dsn) && isPostgresDSNSpace(dsn[i]) {
			i++
		}
		valueStart := i
		value := ""
		if i < len(dsn) && dsn[i] == '\'' {
			i++
			var decoded strings.Builder
			for i < len(dsn) {
				if dsn[i] == '\\' && i+1 < len(dsn) {
					decoded.WriteByte(dsn[i+1])
					i += 2
					continue
				}
				if dsn[i] == '\'' {
					i++
					break
				}
				decoded.WriteByte(dsn[i])
				i++
			}
			value = decoded.String()
		} else {
			for i < len(dsn) && !isPostgresDSNSpace(dsn[i]) {
				i++
			}
			value = dsn[valueStart:i]
		}
		if strings.EqualFold(key, "password") {
			password = value
			continue
		}
		out.WriteString(dsn[keyStart:i])
	}
	return strings.TrimSpace(out.String()), password
}

func isPostgresDSNSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}
