package util

import (
	"bytes"
	"strconv"
)

// FlexInt64 兼容 JSON 数字与字符串形式的整数。
// 部分云厂商在不同版本/地域会混用 "123" 与 123。
type FlexInt64 int64

// UnmarshalJSON 实现 json.Unmarshaler。
func (v *FlexInt64) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*v = 0
		return nil
	}
	if data[0] == '"' {
		data = bytes.Trim(data, `"`)
	}
	number, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return err
	}
	*v = FlexInt64(number)
	return nil
}

// Int64 返回底层整数。
func (v FlexInt64) Int64() int64 { return int64(v) }
