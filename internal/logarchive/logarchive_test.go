package logarchive_test

import (
	"bufio"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lactssl/internal/db"
	"lactssl/internal/logarchive"
	"lactssl/internal/model"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newArchiveTestStore(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-archive-key"))
	if err != nil {
		t.Fatal(err)
	}
	return store.New(conn, cipher)
}

func addOperationLog(t *testing.T, st *store.Store, message string, at time.Time) {
	t.Helper()
	if err := st.AddLogEvent(model.LogEntry{
		Category:  model.LogCategoryOperation,
		Source:    "task",
		RefType:   "task",
		RefID:     7,
		Level:     "info",
		Message:   message,
		Fields:    `{"domain":"example.com"}`,
		RequestID: "req-1",
		CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
}

func addRuntimeLog(t *testing.T, st *store.Store, message string, at time.Time) {
	t.Helper()
	if err := st.AddSystemLogs([]model.SystemLogEntry{{
		Level: "info", Component: "main", Message: message, Fields: `{"n":1}`, CreatedAt: at,
	}}); err != nil {
		t.Fatal(err)
	}
}

func readArchiveRows(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	scanner := bufio.NewScanner(gz)
	rows := make([]map[string]any, 0)
	for scanner.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestClearOperationLogsArchivesBeforeDelete(t *testing.T) {
	st := newArchiveTestStore(t)
	base := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	addOperationLog(t, st, `{"key":"log.task.created","params":{"name":"demo"}}`, base)
	addOperationLog(t, st, "second", base.Add(time.Minute))

	root := t.TempDir()
	svc := logarchive.New(st, logarchive.Options{RootDir: root, Compress: true})
	result, deleted, err := svc.ClearOperationLogs(logarchive.ReasonManualClear, 1, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 || result == nil || result.Rows != 2 {
		t.Fatalf("归档/删除数量不正确: deleted=%d result=%+v", deleted, result)
	}
	if result.File == "" || result.SHA256 == "" || result.Bytes <= 0 {
		t.Fatalf("归档结果缺少文件信息: %+v", result)
	}

	_, total, err := st.ListLogs("", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("操作日志未删除: total=%d", total)
	}

	dataPath := filepath.Join(root, filepath.FromSlash(result.File))
	rows := readArchiveRows(t, dataPath)
	if len(rows) != 2 {
		t.Fatalf("归档行数不正确: %d", len(rows))
	}
	if rows[0]["message"] != `{"key":"log.task.created","params":{"name":"demo"}}` {
		t.Fatalf("归档 message 不应被本地化: %+v", rows[0]["message"])
	}
	if rows[0]["fields"] != `{"domain":"example.com"}` {
		t.Fatalf("归档 fields 未保留原始值: %+v", rows[0]["fields"])
	}
	if rows[0]["request_id"] != "req-1" {
		t.Fatalf("归档 request_id 不正确: %+v", rows[0]["request_id"])
	}

	info, err := os.Stat(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("归档文件权限过宽: %o", info.Mode().Perm())
	}

	entries, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ArchiveID != result.ID || entries[0].Operator.Email != "admin@example.com" {
		t.Fatalf("归档索引不正确: %+v", entries)
	}
	if entries[0].RowCount != 2 || entries[0].File.SHA256 != result.SHA256 {
		t.Fatalf("归档清单不正确: %+v", entries[0])
	}
}

func TestArchiveFailureKeepsLogs(t *testing.T) {
	st := newArchiveTestStore(t)
	addOperationLog(t, st, "keep me", time.Now().UTC())

	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := logarchive.New(st, logarchive.Options{RootDir: blocker, Compress: true})
	if _, _, err := svc.ClearOperationLogs(logarchive.ReasonManualClear, 1, "admin@example.com"); err == nil {
		t.Fatal("归档目录不可用时清空应失败")
	}

	_, total, err := st.ListLogs("", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("归档失败后日志被误删: total=%d", total)
	}
}

func TestClearSystemLogsArchivesAndUpdatesClearedAt(t *testing.T) {
	st := newArchiveTestStore(t)
	now := time.Now().UTC()
	addRuntimeLog(t, st, "old", now.Add(-time.Hour))
	addRuntimeLog(t, st, "fresh", now)

	root := t.TempDir()
	svc := logarchive.New(st, logarchive.Options{RootDir: root, Compress: true})
	result, deleted, err := svc.ClearSystemLogs(logarchive.ReasonManualClear, 2, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 || result == nil || result.Rows != 2 {
		t.Fatalf("系统日志归档/删除数量不正确: deleted=%d result=%+v", deleted, result)
	}

	// 清空后排队中的旧时间戳日志应被丢弃，新日志正常保留。
	addRuntimeLog(t, st, "queued-before-clear", now.Add(-time.Second))
	addRuntimeLog(t, st, "after-clear", time.Now().UTC().Add(time.Second))
	_, total, err := st.ListSystemLogs(store.SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("系统日志清空标记未生效: total=%d", total)
	}
}

func TestPruneSystemLogsByRetentionAndMaxRows(t *testing.T) {
	st := newArchiveTestStore(t)
	now := time.Now().UTC()
	addRuntimeLog(t, st, "expired", now.AddDate(0, 0, -10))
	addRuntimeLog(t, st, "keep", now)

	root := t.TempDir()
	svc := logarchive.New(st, logarchive.Options{RootDir: root, Compress: true})
	results, err := svc.PruneSystemLogs(now.AddDate(0, 0, -1), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Rows != 1 {
		t.Fatalf("按保留期归档结果不正确: %+v", results)
	}
	_, total, err := st.ListSystemLogs(store.SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("按保留期清理结果不正确: total=%d", total)
	}

	addRuntimeLog(t, st, "newer", now.Add(time.Minute))
	results, err = svc.PruneSystemLogs(time.Time{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Reason != logarchive.ReasonMaxRows || results[0].Rows != 1 {
		t.Fatalf("按最大行数归档结果不正确: %+v", results)
	}
	list, total, err := st.ListSystemLogs(store.SystemLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || list[0].Message != "newer" {
		t.Fatalf("按最大行数清理结果不正确: total=%d list=%+v", total, list)
	}
}

func TestListAndDeleteArchive(t *testing.T) {
	st := newArchiveTestStore(t)
	addOperationLog(t, st, "one", time.Now().UTC())

	root := t.TempDir()
	svc := logarchive.New(st, logarchive.Options{RootDir: root, Compress: true})
	result, _, err := svc.ClearOperationLogs(logarchive.ReasonManualClear, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	entry, ok, err := svc.Get(result.ID)
	if err != nil || !ok {
		t.Fatalf("归档不存在: ok=%v err=%v", ok, err)
	}
	if err := svc.Delete(result.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(entry.DataPath); !os.IsNotExist(err) {
		t.Fatalf("归档数据文件未删除: %v", err)
	}
	if _, err := os.Stat(entry.ManifestPath); !os.IsNotExist(err) {
		t.Fatalf("归档清单未删除: %v", err)
	}
	if err := svc.Delete(result.ID); err == nil {
		t.Fatal("重复删除应返回不存在")
	}
}

func TestQuotaFailureKeepsLogs(t *testing.T) {
	st := newArchiveTestStore(t)
	raw := make([]byte, 2<<20)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	addOperationLog(t, st, base64.StdEncoding.EncodeToString(raw), time.Now().UTC())

	root := t.TempDir()
	svc := logarchive.New(st, logarchive.Options{RootDir: root, Compress: true, MaxTotalMB: 1, QuotaPolicy: logarchive.QuotaFail})
	if _, _, err := svc.ClearOperationLogs(logarchive.ReasonManualClear, 1, ""); err == nil {
		t.Fatal("超过归档容量上限时应拒绝删除")
	}
	_, total, err := st.ListLogs("", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("容量超限后日志被误删: total=%d", total)
	}
	entries, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("容量超限的归档未回滚: %+v", entries)
	}
}

func TestStoreMaintenanceArchivesExpiredOperationLogs(t *testing.T) {
	st := newArchiveTestStore(t)
	now := time.Now().UTC()
	addOperationLog(t, st, "expired", now.AddDate(0, 0, -91))
	addOperationLog(t, st, "fresh", now)

	root := t.TempDir()
	svc := logarchive.New(st, logarchive.Options{RootDir: root, Compress: true})
	st.Archiver = svc
	if err := st.Maintenance(); err != nil {
		t.Fatal(err)
	}
	entries, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Reason != logarchive.ReasonRetention || entries[0].RowCount != 1 {
		t.Fatalf("维护任务未生成正确的归档: %+v", entries)
	}
	list, total, err := st.ListLogsFiltered(store.LogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundFresh := false
	for _, entry := range list {
		if entry.Message == "fresh" {
			foundFresh = true
		}
		if entry.Message == "expired" {
			t.Fatalf("过期操作日志未删除: %+v", entry)
		}
	}
	if !foundFresh || total < 1 {
		t.Fatalf("未过期操作日志被误删: total=%d list=%+v", total, list)
	}
}
