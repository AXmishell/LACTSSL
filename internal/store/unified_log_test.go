package store

import (
	"testing"
	"time"

	"lactssl/internal/model"
)

func TestUnifiedLogCategoriesAreIsolated(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()

	st.AddLogKey("task", 7, "info", "log.task.started", map[string]any{"name": "demo"})
	if err := st.AddSystemLogs([]model.SystemLogEntry{{
		Level: "warn", Component: "service", Message: "task failed",
		Fields: `{"task_id":7}`, RefType: "task", RefID: 7, RequestID: "req-1", CreatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}

	// 操作日志查询默认只返回 operation，不应混入运行日志。
	operation, total, err := st.ListLogsFiltered(LogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(operation) != 1 || operation[0].Category != model.LogCategoryOperation {
		t.Fatalf("操作日志查询混入了其他分类: total=%d list=%+v", total, operation)
	}

	runtime, total, err := st.ListLogsFiltered(LogFilter{Category: model.LogCategoryRuntime}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(runtime) != 1 || runtime[0].RefType != "task" || runtime[0].RefID != 7 || runtime[0].RequestID != "req-1" {
		t.Fatalf("运行日志字段不正确: total=%d list=%+v", total, runtime)
	}

	// 系统日志接口只返回 runtime。
	systemLogs, total, err := st.ListSystemLogs(SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || systemLogs[0].Component != "service" {
		t.Fatalf("系统日志查询不正确: total=%d list=%+v", total, systemLogs)
	}

	// 清空系统日志不能影响业务日志。
	deleted, err := st.ClearSystemLogs()
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("清空行数不正确: %d", deleted)
	}
	_, total, err = st.ListLogsFiltered(LogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("清空系统日志误删了业务日志: total=%d", total)
	}
}
