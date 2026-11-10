package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/db"
	"lactssl/internal/model"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

type batchResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Started int    `json:"started"`
		Skipped int    `json:"skipped"`
		Updated int    `json:"updated"`
		Message string `json:"message"`
	} `json:"data"`
}

func newTaskBatchServer(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(conn, cipher)
	s := &Server{Store: st, Manager: service.New(st), limiter: newRateLimiter(100)}
	s.entry.Store("entry")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/tasks/batch/run", s.batchRunTasks)
	r.POST("/tasks/batch/enabled", s.batchSetTasksEnabled)
	return s, r
}

func createBatchTask(t *testing.T, st *store.Store, customize func(*model.Task)) *model.Task {
	t.Helper()
	task := &model.Task{
		Name:      "batch-task",
		Domains:   []string{"example.com"},
		Challenge: "dns01",
		CA:        "letsencrypt",
		KeyType:   "ecdsa",
		AutoRenew: true,
		RenewDays: 30,
		Enabled:   false,
		Status:    "pending",
	}
	if customize != nil {
		customize(task)
	}
	if err := st.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	return task
}

func postBatchJSON(t *testing.T, r http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeBatchResponse(t *testing.T, w *httptest.ResponseRecorder) batchResponse {
	t.Helper()
	var resp batchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v, body=%s", err, w.Body.String())
	}
	return resp
}

func TestBatchSetTasksEnabledWithoutCertificate(t *testing.T) {
	s, r := newTaskBatchServer(t)
	task := createBatchTask(t, s.Store, func(task *model.Task) { task.Enabled = true })

	w := postBatchJSON(t, r, "/tasks/batch/enabled", fmt.Sprintf(`{"ids":[%d],"enabled":false}`, task.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("暂停失败: %d %s", w.Code, w.Body.String())
	}
	if resp := decodeBatchResponse(t, w); resp.Data.Updated != 1 || resp.Data.Skipped != 0 {
		t.Fatalf("暂停结果不正确: %+v", resp.Data)
	}
	paused, err := s.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Enabled {
		t.Fatal("任务应处于暂停状态")
	}
	if paused.NextRunAt != nil {
		t.Fatalf("暂停后不应安排下次执行: %v", paused.NextRunAt)
	}

	w = postBatchJSON(t, r, "/tasks/batch/enabled", fmt.Sprintf(`{"ids":[%d],"enabled":true}`, task.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("恢复失败: %d %s", w.Code, w.Body.String())
	}
	resumed, err := s.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Enabled {
		t.Fatal("任务应处于启用状态")
	}
	if resumed.NextRunAt == nil {
		t.Fatal("没有证书的任务恢复后应立即进入调度队列")
	}
	if diff := time.Since(*resumed.NextRunAt); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("下次执行时间应接近当前时间: %v", resumed.NextRunAt)
	}
}

func TestBatchResumeUsesRenewWindow(t *testing.T) {
	s, r := newTaskBatchServer(t)
	notAfter := time.Now().UTC().Add(90 * 24 * time.Hour)
	cert := &model.Certificate{
		Domains:       []string{"example.com"},
		PrimaryDomain: "example.com",
		NotAfter:      notAfter,
		Status:        "valid",
	}
	if err := s.Store.CreateCertificate(cert); err != nil {
		t.Fatal(err)
	}
	task := createBatchTask(t, s.Store, nil)
	if err := s.Store.UpdateTaskResult(task.ID, "success", "", nil, nil, cert.ID); err != nil {
		t.Fatal(err)
	}

	w := postBatchJSON(t, r, "/tasks/batch/enabled", fmt.Sprintf(`{"ids":[%d],"enabled":true}`, task.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("恢复失败: %d %s", w.Code, w.Body.String())
	}
	resumed, err := s.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.NextRunAt == nil {
		t.Fatal("已有证书的任务应安排续期")
	}
	want := notAfter.AddDate(0, 0, -30)
	if diff := resumed.NextRunAt.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("续期时间不正确: got %v want %v", resumed.NextRunAt, want)
	}
}

func TestBatchRunSkipsRunningAndMissing(t *testing.T) {
	s, r := newTaskBatchServer(t)
	running := createBatchTask(t, s.Store, func(task *model.Task) {
		task.Enabled = true
		task.Status = "running"
	})

	w := postBatchJSON(t, r, "/tasks/batch/run", fmt.Sprintf(`{"ids":[%d,999999]}`, running.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("批量执行请求失败: %d %s", w.Code, w.Body.String())
	}
	resp := decodeBatchResponse(t, w)
	if resp.Data.Started != 0 || resp.Data.Skipped != 2 {
		t.Fatalf("批量执行结果不正确: %+v", resp.Data)
	}
}

func TestTaskBatchRoutesRegistered(t *testing.T) {
	s, _ := newTaskBatchServer(t)
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("注册批量任务路由失败: %v", recovered)
		}
	}()
	_ = s.Router()
}

func TestBatchTaskIDsValidation(t *testing.T) {
	_, r := newTaskBatchServer(t)
	if w := postBatchJSON(t, r, "/tasks/batch/run", `{"ids":[]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("空任务列表应返回 400: %d", w.Code)
	}

	ids := make([]string, 0, maxBatchTasks+1)
	for i := 1; i <= maxBatchTasks+1; i++ {
		ids = append(ids, fmt.Sprintf("%d", i))
	}
	body := fmt.Sprintf(`{"ids":[%s]}`, strings.Join(ids, ","))
	if w := postBatchJSON(t, r, "/tasks/batch/enabled", body); w.Code != http.StatusBadRequest {
		t.Fatalf("超过批量上限应返回 400: %d", w.Code)
	}
}
