package store

import (
	"testing"
	"time"

	"lactssl/internal/model"
)

func TestArchiveSnapshotBoundaries(t *testing.T) {
	st := newTestStore(t)
	base := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		if err := st.AddLogEvent(model.LogEntry{
			Category: model.LogCategoryOperation, Source: "task", Level: "info",
			Message: "row", CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}

	maxID, err := st.MaxLogIDByCategory(model.LogCategoryOperation)
	if err != nil {
		t.Fatal(err)
	}
	if maxID != 4 {
		t.Fatalf("最大 ID 不正确: %d", maxID)
	}

	// 排他上界：beforeID=3 只包含 1、2，快照后新增的 3、4 不会被误删。
	rows, err := st.LogsForArchive(model.LogCategoryOperation, 3, nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
		t.Fatalf("快照读取结果不正确: %+v", rows)
	}
	deleted, err := st.DeleteLogsForArchive(model.LogCategoryOperation, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("按边界删除行数不正确: %d", deleted)
	}
	remaining, total, err := st.ListLogsFiltered(LogFilter{Category: model.LogCategoryOperation}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || remaining[0].ID != 4 || remaining[1].ID != 3 {
		t.Fatalf("边界外日志被误删: total=%d list=%+v", total, remaining)
	}
}

func TestLogBoundaryIDUsesOldestKeptRecord(t *testing.T) {
	st := newTestStore(t)
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if err := st.AddLogEvent(model.LogEntry{
			Category: model.LogCategoryRuntime, Source: "main", Level: "info",
			Message: "row", CreatedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 删除中间记录制造 ID 空洞，边界计算必须继续工作。
	if _, err := st.DB.Exec(`DELETE FROM logs WHERE id IN (2,4)`); err != nil {
		t.Fatal(err)
	}

	boundary, err := st.LogBoundaryID(model.LogCategoryRuntime, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 保留 id=3、5，最旧保留记录是 3；删除 id<3 只应移除 id=1。
	if boundary != 3 {
		t.Fatalf("边界 ID 不正确: %d", boundary)
	}
	if _, err := st.DeleteLogsForArchive(model.LogCategoryRuntime, boundary, nil); err != nil {
		t.Fatal(err)
	}
	list, total, err := st.ListSystemLogs(SystemLogFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || list[0].ID != 5 || list[1].ID != 3 {
		t.Fatalf("按最大行数删除结果不正确: total=%d list=%+v", total, list)
	}
}

func TestLogsForArchiveCutoff(t *testing.T) {
	st := newTestStore(t)
	base := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := st.AddLogEvent(model.LogEntry{
			Category: model.LogCategoryOperation, Source: "task", Level: "info",
			Message: "row", CreatedAt: base.Add(time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := base.Add(90 * time.Minute)
	rows, err := st.LogsForArchive(model.LogCategoryOperation, 4, &cutoff, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != 1 || rows[1].ID != 2 {
		t.Fatalf("保留期快照结果不正确: %+v", rows)
	}
}
