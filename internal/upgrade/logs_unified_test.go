package upgrade

import (
	"context"
	"testing"
	"time"

	"lactssl/internal/model"
	"lactssl/internal/store"
)

func TestStepLogsUnifiedCopiesSystemLogsOnce(t *testing.T) {
	manager, st, _, _ := newTestManager(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := st.DB.Exec(
		`INSERT INTO system_logs(level,component,message,fields,created_at) VALUES(?,?,?,?,?)`,
		"WARNING", " service ", "task failed", `{"task_id":7}`, now,
	); err != nil {
		t.Fatalf("写入旧系统日志失败: %v", err)
	}
	if _, err := st.DB.Exec(
		`INSERT INTO logs(source,ref_id,level,message,created_at) VALUES('task',9,'info','old operation',?)`,
		now,
	); err != nil {
		t.Fatalf("写入旧操作日志失败: %v", err)
	}

	result, err := stepLogsUnified(ctx, manager)
	if err != nil {
		t.Fatalf("合并日志失败: %v", err)
	}
	if !result.Changed {
		t.Fatalf("应报告复制了日志: %+v", result)
	}

	list, total, err := st.ListSystemLogs(store.SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].Message != "task failed" {
		t.Fatalf("系统日志未进入统一表: total=%d list=%+v", total, list)
	}
	if list[0].Level != "warn" || list[0].Component != "service" {
		t.Fatalf("级别或组件未归一: %+v", list[0])
	}
	var category string
	if err := st.DB.QueryRow(`SELECT category FROM logs WHERE message='task failed'`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if category != model.LogCategoryRuntime {
		t.Fatalf("分类不正确: %q", category)
	}
	var refType string
	if err := st.DB.QueryRow(`SELECT ref_type FROM logs WHERE message='old operation'`).Scan(&refType); err != nil {
		t.Fatal(err)
	}
	if refType != "task" {
		t.Fatalf("旧操作日志 ref_type 未回填: %q", refType)
	}

	// 重复执行不应产生重复记录。
	if _, err := stepLogsUnified(ctx, manager); err != nil {
		t.Fatalf("重复合并日志失败: %v", err)
	}
	_, total, err = st.ListSystemLogs(store.SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("重复执行产生了重复日志: total=%d", total)
	}
}
