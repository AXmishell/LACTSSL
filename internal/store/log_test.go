package store

import (
	"testing"
	"time"
)

func insertTestLog(t *testing.T, st *Store, source string, refID int64, level, message string, at time.Time) int64 {
	t.Helper()
	result, err := st.DB.Exec(
		`INSERT INTO logs(source,ref_id,level,message,created_at) VALUES(?,?,?,?,?)`,
		source, refID, level, message, at,
	)
	if err != nil {
		t.Fatalf("写入测试日志失败: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("读取测试日志 ID 失败: %v", err)
	}
	return id
}

func TestListLogsFiltered(t *testing.T) {
	st := newTestStore(t)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	insertTestLog(t, st, "task", 1, "info", "task info", base)
	insertTestLog(t, st, "task", 2, "error", "task error", base.Add(time.Hour))
	insertTestLog(t, st, "deploy", 3, "success", "deploy success", base.Add(2*time.Hour))
	insertTestLog(t, st, "task", 4, "warning", "task warning", base.Add(3*time.Hour))

	list, total, err := st.ListLogsFiltered(LogFilter{Source: "task"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(list) != 3 {
		t.Fatalf("按来源筛选结果不正确: total=%d len=%d", total, len(list))
	}
	if list[0].Message != "task warning" || list[2].Message != "task info" {
		t.Fatalf("日志排序不正确: %+v", list)
	}

	list, total, err = st.ListLogsFiltered(LogFilter{Level: "error"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].Source != "task" {
		t.Fatalf("按级别筛选结果不正确: total=%d list=%+v", total, list)
	}

	from := base.Add(90 * time.Minute)
	to := base.Add(150 * time.Minute)
	list, total, err = st.ListLogsFiltered(LogFilter{From: &from, To: &to}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || list[0].Message != "deploy success" {
		t.Fatalf("按时间筛选结果不正确: total=%d list=%+v", total, list)
	}

	list, total, err = st.ListLogsFiltered(LogFilter{Source: "task", Level: "warning", From: &from, To: &to}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("组合筛选结果不正确: total=%d list=%+v", total, list)
	}

	legacy, legacyTotal, err := st.ListLogs("deploy", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if legacyTotal != 1 || len(legacy) != 1 || legacy[0].Message != "deploy success" {
		t.Fatalf("ListLogs 兼容调用结果不正确: total=%d list=%+v", legacyTotal, legacy)
	}
}

func TestExportLogsSnapshotAndCursor(t *testing.T) {
	st := newTestStore(t)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		insertTestLog(t, st, "task", int64(i+1), "info", "entry", base.Add(time.Duration(i)*time.Minute))
	}
	maxID, err := st.MaxLogID()
	if err != nil {
		t.Fatal(err)
	}
	if maxID != 5 {
		t.Fatalf("最大日志 ID 不正确: %d", maxID)
	}

	first, err := st.ExportLogs(LogFilter{}, maxID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID != 5 || first[1].ID != 4 {
		t.Fatalf("首批导出结果不正确: %+v", first)
	}

	second, err := st.ExportLogs(LogFilter{}, maxID, first[len(first)-1].ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 2 || second[0].ID != 3 || second[1].ID != 2 {
		t.Fatalf("第二批导出结果不正确: %+v", second)
	}

	third, err := st.ExportLogs(LogFilter{}, maxID, second[len(second)-1].ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 1 || third[0].ID != 1 {
		t.Fatalf("最后一批导出结果不正确: %+v", third)
	}

	// 快照上界之后新增的日志不应出现在导出结果中。
	insertTestLog(t, st, "task", 6, "info", "new entry", base.Add(time.Hour))
	after, err := st.ExportLogs(LogFilter{}, maxID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 5 || after[0].ID != 5 {
		t.Fatalf("导出未遵守快照上界: %+v", after)
	}

	empty, err := st.ExportLogs(LogFilter{}, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("空快照应返回空结果: %+v", empty)
	}
}

func TestMaxLogIDOnEmptyTable(t *testing.T) {
	st := newTestStore(t)
	id, err := st.MaxLogID()
	if err != nil {
		t.Fatal(err)
	}
	if id != 0 {
		t.Fatalf("空表最大 ID 应为 0，实际 %d", id)
	}
}
