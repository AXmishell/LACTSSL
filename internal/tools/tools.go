//go:build tools

// Package tools 固定不会被主程序直接导入、但需要保持在安全版本之上的间接依赖。
// go mod tidy 会读取该文件的 import，从而避免安全升级在下次 tidy 时被回退。
package tools

import (
	_ "go.opentelemetry.io/otel/log"
	_ "go.opentelemetry.io/otel/sdk"
	_ "go.opentelemetry.io/otel/sdk/metric"
)
