package db

import (
	"testing"
	"time"
)

func TestNormalizeTimeArgs(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	original := time.Date(2026, 11, 23, 12, 0, 0, 0, location)
	args := []any{"x", original}
	normalized := normalizeTimeArgs(args)
	if len(normalized) != 2 {
		t.Fatalf("参数数量=%d", len(normalized))
	}
	if got := normalized[1].(time.Time); got.Location() != time.UTC || !got.Equal(original) {
		t.Fatalf("时间参数未转换为 UTC: %s", got)
	}
	if args[1].(time.Time).Location() != location {
		t.Fatal("不应修改调用方传入的参数切片")
	}
	if got := normalizeTimeArgs([]any{1, "x"}); len(got) != 2 {
		t.Fatalf("不含时间参数时应原样返回，得到 %d 项", len(got))
	}
	pointer := original
	normalized = normalizeTimeArgs([]any{&pointer})
	if got := normalized[0].(time.Time); got.Location() != time.UTC {
		t.Fatalf("时间指针参数未转换为 UTC: %s", got)
	}
	if got := normalizeTimeArgs([]any{(*time.Time)(nil)}); got[0] != (*time.Time)(nil) {
		t.Fatal("nil 时间指针应保持不变")
	}
}

func TestPoolLimitsKeepsRoomForLockConnection(t *testing.T) {
	maxOpen, maxIdle := poolLimits(Options{MaxOpenConns: 1, MaxIdleConns: 1}, 2)
	if maxOpen != 2 || maxIdle != 1 {
		t.Fatalf("poolLimits = %d/%d，期望 2/1", maxOpen, maxIdle)
	}
	maxOpen, maxIdle = poolLimits(Options{MaxOpenConns: 10, MaxIdleConns: 20}, 1)
	if maxOpen != 10 || maxIdle != 10 {
		t.Fatalf("poolLimits = %d/%d，期望 10/10", maxOpen, maxIdle)
	}
}

func TestMySQLLockNameIsScopedAndBounded(t *testing.T) {
	first := mysqlLockNameFor("lactssl_a")
	second := mysqlLockNameFor("lactssl_b")
	if first == second {
		t.Fatal("不同数据库应使用不同锁名")
	}
	long := mysqlLockNameFor("a-very-long-database-name-that-exceeds-the-mysql-lock-name-limit-0123456789")
	if len(long) > 64 {
		t.Fatalf("锁名长度=%d，超过 MySQL GET_LOCK 上限 64", len(long))
	}
}
