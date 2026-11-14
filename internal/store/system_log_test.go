package store

import (
	"testing"
	"time"

	"lactssl/internal/model"
)

func insertSystemLog(t *testing.T, st *Store, level, component, message, fields string, at time.Time) {
	t.Helper()
	if err := st.AddSystemLogs([]model.SystemLogEntry{{
		Level: level, Component: component, Message: message, Fields: fields, CreatedAt: at,
	}}); err != nil {
		t.Fatalf("写入系统日志失败: %v", err)
	}
}

func TestListSystemLogsFiltered(t *testing.T) {
	st := newTestStore(t)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	insertSystemLog(t, st, "info", "main", "startup", `{"version":"dev"}`, base)
	insertSystemLog(t, st, "warn", "service", "task failed", `{"task_id":7}`, base.Add(time.Hour))
	insertSystemLog(t, st, "error", "http", "panic recovered", `{"request_id":"abc"}`, base.Add(2*time.Hour))
	insertSystemLog(t, st, "info", "acme", "acme request", `{"status":400}`, base.Add(3*time.Hour))

	list, total, err := st.ListSystemLogs(SystemLogFilter{Level: "warn"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].Component != "service" {
		t.Fatalf("按级别筛选结果不正确: total=%d list=%+v", total, list)
	}

	list, total, err = st.ListSystemLogs(SystemLogFilter{Component: "http"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || list[0].Message != "panic recovered" {
		t.Fatalf("按组件筛选结果不正确: total=%d list=%+v", total, list)
	}

	// 关键字同时匹配 message 与 fields 内的 JSON 文本。
	list, total, err = st.ListSystemLogs(SystemLogFilter{Query: "task_id"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || list[0].Level != "warn" {
		t.Fatalf("关键字筛选结果不正确: total=%d list=%+v", total, list)
	}

	from := base.Add(90 * time.Minute)
	to := base.Add(150 * time.Minute)
	list, total, err = st.ListSystemLogs(SystemLogFilter{From: &from, To: &to}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || list[0].Message != "panic recovered" {
		t.Fatalf("按时间筛选结果不正确: total=%d list=%+v", total, list)
	}

	// LIKE 通配符按字面量处理。
	list, total, err = st.ListSystemLogs(SystemLogFilter{Query: "%"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("通配符应被转义: total=%d list=%+v", total, list)
	}

	components, err := st.SystemLogComponents()
	if err != nil {
		t.Fatal(err)
	}
	if len(components) != 4 {
		t.Fatalf("组件列表不正确: %+v", components)
	}
}

func TestSystemLogCleanupAndClear(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	insertSystemLog(t, st, "info", "main", "old", "", now.AddDate(0, 0, -10))
	insertSystemLog(t, st, "info", "main", "fresh", "", now)
	if err := st.CleanupSystemLogs(); err != nil {
		t.Fatal(err)
	}
	_, total, err := st.ListSystemLogs(SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("过期日志未被清理: total=%d", total)
	}

	// 行数上限触发时保留最新记录。
	st.SystemLogRetentionDays = 0
	st.SystemLogMaxRows = 1
	insertSystemLog(t, st, "info", "main", "newer", "", now.Add(time.Minute))
	if err := st.CleanupSystemLogs(); err != nil {
		t.Fatal(err)
	}
	list, total, err := st.ListSystemLogs(SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || list[0].Message != "newer" {
		t.Fatalf("行数上限清理不正确: total=%d list=%+v", total, list)
	}

	deleted, err := st.ClearSystemLogs()
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("清空行数不正确: %d", deleted)
	}

	// 清空前排队中的旧日志不应在清空后重新写回；清空后产生的新日志正常保留。
	insertSystemLog(t, st, "info", "main", "queued-before-clear", "", now.Add(-time.Second))
	insertSystemLog(t, st, "info", "main", "after-clear", "", time.Now().UTC().Add(time.Second))
	list, total, err = st.ListSystemLogs(SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || list[0].Message != "after-clear" {
		t.Fatalf("清空标记未生效: total=%d list=%+v", total, list)
	}
}

func TestExportSystemLogsSnapshotAndCursor(t *testing.T) {
	st := newTestStore(t)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	insertSystemLog(t, st, "info", "main", "one", `{"n":1}`, base)
	insertSystemLog(t, st, "warn", "service", "two", `{"n":2}`, base.Add(time.Hour))
	insertSystemLog(t, st, "error", "http", "three", `{"n":3}`, base.Add(2*time.Hour))
	// 模拟导出开始后新增的日志，不应出现在固定快照中。
	maxID, err := st.MaxSystemLogID()
	if err != nil {
		t.Fatal(err)
	}
	insertSystemLog(t, st, "info", "main", "after-snapshot", "", base.Add(3*time.Hour))

	first, err := st.ExportSystemLogs(SystemLogFilter{}, maxID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].Message != "three" || first[1].Message != "two" {
		t.Fatalf("导出首批结果不正确: %+v", first)
	}
	second, err := st.ExportSystemLogs(SystemLogFilter{}, maxID, first[len(first)-1].ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Message != "one" {
		t.Fatalf("导出游标结果不正确: %+v", second)
	}
	filtered, err := st.ExportSystemLogs(SystemLogFilter{Level: "warn"}, maxID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Component != "service" {
		t.Fatalf("导出筛选结果不正确: %+v", filtered)
	}
}

func TestAddSystemLogsTruncatesLongValues(t *testing.T) {
	st := newTestStore(t)
	long := make([]rune, 3000)
	for index := range long {
		long[index] = 'a'
	}
	insertSystemLog(t, st, "INFO", " component ", string(long), `{"stack":"`+string(long)+`"}`, time.Now().UTC())
	list, _, err := st.ListSystemLogs(SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("unexpected list: %+v", list)
	}
	if list[0].Level != "info" || list[0].Component != "component" {
		t.Fatalf("level/component not normalized: %+v", list[0])
	}
	if len([]rune(list[0].Message)) != 2000 {
		t.Fatalf("message not truncated: %d", len([]rune(list[0].Message)))
	}
}
