package db

import (
	"regexp"
	"strings"
)

// Dialect 表示数据库方言。
type Dialect string

const (
	// SQLite 是默认的嵌入式数据库。
	SQLite Dialect = "sqlite"
	// Postgres 是 PostgreSQL 方言。
	Postgres Dialect = "postgres"
	// MySQL 是 MySQL 5.7+ 方言（同时兼容 MariaDB 10.2+ 的常用语法）。
	MySQL Dialect = "mysql"
)

// String 返回配置中使用的方言名称。
func (d Dialect) String() string {
	if d == "" {
		return string(SQLite)
	}
	return string(d)
}

// IsPostgres 判断是否为 PostgreSQL。
func (d Dialect) IsPostgres() bool { return d == Postgres }

// IsMySQL 判断是否为 MySQL / MariaDB。
func (d Dialect) IsMySQL() bool { return d == MySQL }

// ParseDialect 解析并规范化方言名称。
func ParseDialect(value string) (Dialect, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "sqlite", "sqlite3":
		return SQLite, true
	case "postgres", "postgresql", "pg":
		return Postgres, true
	case "mysql", "mariadb":
		return MySQL, true
	default:
		return "", false
	}
}

// Rebind 把 SQL 中的 ? 占位符转换为 Postgres 的 $1、$2……
// SQLite 与 MySQL 原样返回。MySQL 额外把 PostgreSQL 风格的
// ON CONFLICT ... DO NOTHING / DO UPDATE SET 改写为 ON DUPLICATE KEY UPDATE，
// 这样业务 SQL 可以只维护一份写法。
func (d Dialect) Rebind(query string) string {
	switch d {
	case Postgres:
		return rebindDollar(query)
	case MySQL:
		return rebindMySQL(query)
	default:
		return query
	}
}

// QuoteIdent 按方言引用标识符。
func (d Dialect) QuoteIdent(name string) string {
	if d == MySQL {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

var (
	onConflictNothingPattern = regexp.MustCompile(`(?is)\s+ON\s+CONFLICT\s*\(([^)]*)\)\s+DO\s+NOTHING\s*$`)
	onConflictUpdatePattern  = regexp.MustCompile(`(?is)\s+ON\s+CONFLICT\s*\(([^)]*)\)\s+DO\s+UPDATE\s+SET\s+(.+)$`)
	excludedColumnPattern    = regexp.MustCompile(`(?i)\bexcluded\.([a-z_][a-z0-9_]*)`)
)

// rebindMySQL 把 PostgreSQL 的冲突处理语法转换为 MySQL 写法。
// 只处理本项目实际使用的简单形式；其它语句保持原样。
func rebindMySQL(query string) string {
	if match := onConflictNothingPattern.FindStringSubmatchIndex(query); match != nil {
		columns := query[match[2]:match[3]]
		first := firstColumnName(columns)
		if first == "" {
			return query[:match[1]] + " ON DUPLICATE KEY UPDATE id=id"
		}
		quoted := MySQL.QuoteIdent(first)
		return query[:match[0]] + " ON DUPLICATE KEY UPDATE " + quoted + "=" + quoted
	}
	if match := onConflictUpdatePattern.FindStringSubmatchIndex(query); match != nil {
		assignments := query[match[4]:match[5]]
		assignments = excludedColumnPattern.ReplaceAllStringFunc(assignments, func(value string) string {
			parts := excludedColumnPattern.FindStringSubmatch(value)
			if len(parts) < 2 {
				return value
			}
			return "VALUES(" + MySQL.QuoteIdent(parts[1]) + ")"
		})
		return query[:match[0]] + " ON DUPLICATE KEY UPDATE " + assignments
	}
	return query
}

// firstColumnName 从 "a, b" 或 "a,b" 中取出第一列名。
func firstColumnName(columns string) string {
	if index := strings.IndexByte(columns, ','); index >= 0 {
		columns = columns[:index]
	}
	return strings.TrimSpace(columns)
}

// rebindDollar 实现 ? -> $n 的词法安全转换。
func rebindDollar(query string) string {
	var out strings.Builder
	out.Grow(len(query) + 8)
	index := 1
	for i := 0; i < len(query); {
		switch query[i] {
		case '\'':
			// 单引号字符串，'' 表示转义。
			out.WriteByte(query[i])
			i++
			for i < len(query) {
				out.WriteByte(query[i])
				if query[i] == '\'' {
					if i+1 < len(query) && query[i+1] == '\'' {
						out.WriteByte(query[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case '"':
			// 双引号标识符，"" 表示转义。
			out.WriteByte(query[i])
			i++
			for i < len(query) {
				out.WriteByte(query[i])
				if query[i] == '"' {
					if i+1 < len(query) && query[i+1] == '"' {
						out.WriteByte(query[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case '$':
			// 美元引号：$tag$ ... $tag$
			if tag, ok := dollarQuoteTag(query[i:]); ok {
				end := i + len(tag)
				out.WriteString(tag)
				closing := strings.Index(query[end:], tag)
				if closing < 0 {
					out.WriteString(query[end:])
					return out.String()
				}
				out.WriteString(query[end : end+closing+len(tag)])
				i = end + closing + len(tag)
				continue
			}
			out.WriteByte(query[i])
			i++
		case '-':
			if i+1 < len(query) && query[i+1] == '-' {
				end := strings.IndexByte(query[i:], '\n')
				if end < 0 {
					out.WriteString(query[i:])
					return out.String()
				}
				out.WriteString(query[i : i+end+1])
				i += end + 1
				continue
			}
			out.WriteByte(query[i])
			i++
		case '/':
			if i+1 < len(query) && query[i+1] == '*' {
				end := strings.Index(query[i+2:], "*/")
				if end < 0 {
					out.WriteString(query[i:])
					return out.String()
				}
				end += i + 2 + 2
				out.WriteString(query[i:end])
				i = end
				continue
			}
			out.WriteByte(query[i])
			i++
		case '?':
			out.WriteByte('$')
			out.WriteString(itoa(index))
			index++
			i++
		default:
			out.WriteByte(query[i])
			i++
		}
	}
	return out.String()
}

// dollarQuoteTag 返回从 $ 开始的美元引号标签（含首尾 $），例如 $tag$。
func dollarQuoteTag(s string) (string, bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", false
	}
	if s[1] == '$' {
		return "$$", true
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '$' {
			return s[:i+1], true
		}
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return "", false
		}
	}
	return "", false
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[pos:])
}

// isWriteSQL 判断语句是否会修改数据。用于切换期间的写门禁。
// 读取型 CTE（WITH ... SELECT）不会命中；当前代码没有写型 CTE。
func isWriteSQL(query string) bool {
	s := strings.TrimSpace(query)
	for strings.HasPrefix(s, "(") {
		s = strings.TrimSpace(s[1:])
	}
	upper := strings.ToUpper(s)
	for _, prefix := range []string{
		"INSERT", "UPDATE", "DELETE", "REPLACE", "ALTER", "CREATE", "DROP",
		"TRUNCATE", "VACUUM", "ANALYZE", "PRAGMA", "GRANT", "REVOKE",
	} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}
