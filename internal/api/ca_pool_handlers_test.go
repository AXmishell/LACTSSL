package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"lactssl/internal/model"
	"lactssl/internal/service"
)

func TestCAPoolCRUDAndTaskReference(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	headers := map[string]string{csrfHeaderName: "1"}

	account := &model.DNSAccount{Name: "test", Provider: "cloudflare", Config: map[string]string{}}
	if err := st.CreateDNSAccount(account); err != nil {
		t.Fatal(err)
	}

	w := sessionRequest(t, router, http.MethodPost, "/api/ca-pools",
		`{"name":"production","strategy":"round_robin","enabled":true,"items":[{"ca_value":"letsencrypt","priority":10,"weight":1,"enabled":true},{"ca_value":"zerossl","priority":20,"weight":1,"enabled":true}]}`,
		[]*http.Cookie{session}, headers)
	if w.Code != http.StatusOK {
		t.Fatalf("创建调度组失败: %d %s", w.Code, w.Body.String())
	}
	var pool model.CAPool
	if err := json.Unmarshal([]byte(extractData(t, w.Body.String())), &pool); err != nil {
		t.Fatal(err)
	}
	if pool.ID == 0 || len(pool.Items) != 2 || pool.PublicID == "" {
		t.Fatalf("创建结果不完整: %+v", pool)
	}

	w = sessionRequest(t, router, http.MethodGet, "/api/ca-pools", "", []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("列表失败: %d %s", w.Code, w.Body.String())
	}

	taskBody := `{"name":"pool task","domains":["example.com"],"dns_account_id":` +
		strconv.FormatInt(account.ID, 10) +
		`,"ca_pool_id":` + strconv.FormatInt(pool.ID, 10) +
		`,"key_type":"ecdsa","auto_renew":false,"renew_days":30,"enabled":false}`
	w = sessionRequest(t, router, http.MethodPost, "/api/tasks", taskBody, []*http.Cookie{session}, headers)
	if w.Code != http.StatusOK {
		t.Fatalf("创建调度组任务失败: %d %s", w.Code, w.Body.String())
	}
	var task model.Task
	if err := json.Unmarshal([]byte(extractData(t, w.Body.String())), &task); err != nil {
		t.Fatal(err)
	}
	if task.CAPoolID != pool.ID || task.CA != "" {
		t.Fatalf("任务未保存调度组: %+v", task)
	}

	w = sessionRequest(t, router, http.MethodDelete, "/api/ca-pools/"+strconv.FormatInt(pool.ID, 10), "", []*http.Cookie{session}, headers)
	if w.Code != http.StatusConflict {
		t.Fatalf("被任务引用时应拒绝删除: %d %s", w.Code, w.Body.String())
	}

	if err := st.SetSetting(service.CAPoolSettingKey, strconv.FormatInt(pool.ID, 10)); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	w = sessionRequest(t, router, http.MethodDelete, "/api/ca-pools/"+strconv.FormatInt(pool.ID, 10), "", []*http.Cookie{session}, headers)
	if w.Code != http.StatusConflict {
		t.Fatalf("默认调度组应拒绝删除: %d %s", w.Code, w.Body.String())
	}
	if err := st.SetSetting(service.CAPoolSettingKey, "0"); err != nil {
		t.Fatal(err)
	}
	w = sessionRequest(t, router, http.MethodDelete, "/api/ca-pools/"+strconv.FormatInt(pool.ID, 10), "", []*http.Cookie{session}, headers)
	if w.Code != http.StatusOK {
		t.Fatalf("解除引用后应能删除: %d %s", w.Code, w.Body.String())
	}
}

func TestCAPoolValidation(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	session := findCookie(t, setup.Result().Cookies(), sessionCookieName)
	headers := map[string]string{csrfHeaderName: "1"}

	cases := []string{
		`{"name":"empty","items":[]}`,
		`{"name":"duplicate","items":[{"ca_value":"letsencrypt","enabled":true},{"ca_value":"letsencrypt","enabled":true}]}`,
		`{"name":"private","items":[{"ca_value":"private-ca:1","enabled":true}]}`,
		`{"name":"invalid","items":[{"ca_value":"acme-ca:9999","enabled":true}]}`,
		`{"name":"bad strategy","strategy":"nope","items":[{"ca_value":"letsencrypt","enabled":true}]}`,
	}
	for _, body := range cases {
		w := sessionRequest(t, router, http.MethodPost, "/api/ca-pools", body, []*http.Cookie{session}, headers)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("非法请求应被拒绝: %s -> %d %s", body, w.Code, w.Body.String())
		}
	}
}
