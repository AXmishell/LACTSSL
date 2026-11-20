package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/service"
)

func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "API Test Root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestTrustedCAImportAndDelete(t *testing.T) {
	st := newSessionTestStore(t)
	gin.SetMode(gin.TestMode)
	s := New(
		st,
		auth.New([]byte("jwt-secret")),
		service.New(st),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}},
		nil,
	)
	s.DataDir = t.TempDir()
	router := s.Router()

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	cookies := setup.Result().Cookies()
	session := findCookie(t, cookies, sessionCookieName)
	csrf := findCookie(t, cookies, csrfCookieName)
	headers := map[string]string{csrfHeaderName: csrf.Value}

	certPEM := testCAPEM(t)
	body, _ := json.Marshal(map[string]string{"name": "Corp Root", "cert_pem": certPEM})
	created := sessionRequest(t, router, http.MethodPost, "/api/trusted-cas/import",
		string(body), []*http.Cookie{session}, headers)
	if created.Code != http.StatusOK {
		t.Fatalf("导入 CA 失败: %d %s", created.Code, created.Body.String())
	}
	if data, err := os.ReadFile(filepath.Join(s.DataDir, "trusted-ca-bundle.pem")); err != nil {
		t.Fatalf("信任链文件未生成: %v", err)
	} else if !strings.Contains(string(data), "BEGIN CERTIFICATE") {
		t.Fatal("信任链文件内容不正确")
	}

	list := sessionRequest(t, router, http.MethodGet, "/api/trusted-cas", "", []*http.Cookie{session}, nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Corp Root") {
		t.Fatalf("列表未返回导入的 CA: %d %s", list.Code, list.Body.String())
	}

	duplicate := sessionRequest(t, router, http.MethodPost, "/api/trusted-cas/import",
		string(body), []*http.Cookie{session}, headers)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("重复导入应返回 409: %d %s", duplicate.Code, duplicate.Body.String())
	}

	items, err := s.Store.ListTrustedCAs()
	if err != nil || len(items) != 1 {
		t.Fatalf("数据库记录不正确: %v %+v", err, items)
	}
	deleted := sessionRequest(t, router, http.MethodDelete, "/api/trusted-cas/1", "", []*http.Cookie{session}, headers)
	if deleted.Code != http.StatusOK {
		t.Fatalf("删除 CA 失败: %d %s", deleted.Code, deleted.Body.String())
	}
	items, err = s.Store.ListTrustedCAs()
	if err != nil || len(items) != 0 {
		t.Fatalf("删除后仍有记录: %v %+v", err, items)
	}
}

func TestPrivateCAIncludedInTrustBundle(t *testing.T) {
	st := newSessionTestStore(t)
	gin.SetMode(gin.TestMode)
	s := New(
		st,
		auth.New([]byte("jwt-secret")),
		service.New(st),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(sessionIndexHTML)}},
		nil,
	)
	s.DataDir = t.TempDir()
	router := s.Router()

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	cookies := setup.Result().Cookies()
	session := findCookie(t, cookies, sessionCookieName)
	csrf := findCookie(t, cookies, csrfCookieName)
	headers := map[string]string{csrfHeaderName: csrf.Value}

	created := sessionRequest(t, router, http.MethodPost, "/api/private-cas",
		`{"name":"Private Trust","common_name":"Private Trust CA","key_type":"ecdsa","valid_days":30}`,
		[]*http.Cookie{session}, headers)
	if created.Code != http.StatusOK {
		t.Fatalf("创建私有 CA 失败: %d %s", created.Code, created.Body.String())
	}

	raw := sessionRequest(t, router, http.MethodGet, "/api/trusted-cas/bundle", "", []*http.Cookie{session}, nil)
	if raw.Code != http.StatusOK {
		t.Fatalf("下载信任链失败: %d %s", raw.Code, raw.Body.String())
	}
	found := false
	remaining := raw.Body.Bytes()
	for {
		block, rest := pem.Decode(remaining)
		if block == nil {
			break
		}
		remaining = rest
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if cert.Subject.CommonName == "Private Trust CA" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("信任链应包含新建私有 CA")
	}
}
