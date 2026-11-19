package db

import (
	"strings"
)

// Dialect 表示数据库方言。
type Dialect string

const (
	// SQLite 是默认的嵌入式数据库。
	SQLite Dialect = "sqlite"
	// Postgres 是 PostgreSQL 方言。
	Postgres Dialect = "postgres"
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

// ParseDialect 解析并规范化方言名称。
func ParseDialect(value string) (Dialect, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "sqlite", "sqlite3":
		return SQLite, true
	case "postgres", "postgresql", "pg":
		return Postgres, true
	default:
		return "", false
	}
}

// Rebind 把 SQL 中的 ? 占位符转换为 Postgres 的 $1、$2……
// SQLite 原样返回。字符串字面量、引号标识符、美元引号与注释内的 ? 不转换。
func (d Dialect) Rebind(query string) string {
	if d != Postgres {
		return query
	}
	return rebindDollar(query)
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

// isWriteSQL 判断语句是否会修改数据。用于转换期间的写门禁。
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
