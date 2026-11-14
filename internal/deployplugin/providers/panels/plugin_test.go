package panels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lactssl/internal/deployplugin"
	"lactssl/internal/model"
)

func TestBaotaRequestToken(t *testing.T) {
	if got := baotaRequestToken("secret", "1700000000"); got != "a9928977fac385df0305b03f9d039e65" {
		t.Fatalf("unexpected BaoTa request token: %q", got)
	}
}

func TestMd5Hex(t *testing.T) {
	if got := md5Hex("1panel" + "test-key" + "1700000000"); got != "f360f407f5ac39bdb7706111e1cc304f" {
		t.Fatalf("unexpected md5 value: %q", got)
	}
}

func TestDeployBaotaSite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/site" || r.URL.Query().Get("action") != "SetSSL" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("invalid form: %v", err)
		}
		if r.Form.Get("request_token") != baotaRequestToken("secret", r.Form.Get("request_time")) {
			t.Errorf("invalid request token")
		}
		if r.Form.Get("siteName") != "example.com" {
			t.Errorf("unexpected site name: %q", r.Form.Get("siteName"))
		}
		if r.Form.Get("csr") != "FULLCHAIN" || r.Form.Get("key") != "PRIVATE-KEY" {
			t.Errorf("certificate content was not submitted")
		}
		_, _ = w.Write([]byte(`{"status":true,"msg":"ok"}`))
	}))
	defer server.Close()

	cert := &model.Certificate{
		PrimaryDomain: "example.com",
		FullchainPEM:  "FULLCHAIN",
		KeyPEM:        "PRIVATE-KEY",
	}
	cfg := map[string]string{
		"panel_url":  server.URL,
		"api_secret": "secret",
		"sites":      "example.com",
	}
	if err := deployBaotaSite(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDeployBaotaPanel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/config" || r.URL.Query().Get("action") != "SavePanelSSL" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("invalid form: %v", err)
		}
		if r.Form.Get("certPem") != "FULLCHAIN" || r.Form.Get("privateKey") != "PRIVATE-KEY" {
			t.Errorf("panel certificate content was not submitted")
		}
		_, _ = w.Write([]byte(`{"status":true,"msg":"ok"}`))
	}))
	defer server.Close()

	cert := &model.Certificate{FullchainPEM: "FULLCHAIN", KeyPEM: "PRIVATE-KEY"}
	cfg := map[string]string{"panel_url": server.URL, "api_secret": "secret"}
	if err := deployBaotaPanel(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDeployOnePanelSite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timestamp := r.Header.Get("1Panel-Timestamp")
		if got := r.Header.Get("1Panel-Token"); got != md5Hex("1panel"+"test-key"+timestamp) {
			t.Errorf("unexpected 1Panel token: %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/websites/search":
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("invalid search payload: %v", err)
			}
			if payload["name"] != "example.com" {
				t.Errorf("unexpected search name: %v", payload["name"])
			}
			_, _ = w.Write([]byte(`{"code":200,"data":{"total":1,"items":[{"id":7,"primaryDomain":"example.com","alias":"demo"}]}}`))
		case "/api/v2/websites/7/https":
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("invalid https payload: %v", err)
			}
			if payload["websiteId"] != float64(7) || payload["type"] != "manual" || payload["importType"] != "paste" {
				t.Errorf("unexpected https payload: %v", payload)
			}
			if payload["certificate"] != "FULLCHAIN" || payload["privateKey"] != "PRIVATE-KEY" || payload["enable"] != true {
				t.Errorf("certificate was not submitted: %v", payload)
			}
			_, _ = w.Write([]byte(`{"code":200,"data":null}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := &model.Certificate{PrimaryDomain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "PRIVATE-KEY"}
	cfg := map[string]string{
		"panel_url":   server.URL,
		"api_key":     "test-key",
		"api_version": "v2",
		"website":     "example.com",
	}
	if err := deployOnePanelSite(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDeployOnePanelPanel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/core/settings/ssl/update" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("invalid panel payload: %v", err)
		}
		if payload["cert"] != "FULLCHAIN" || payload["key"] != "PRIVATE-KEY" || payload["domain"] != "panel.example.com" {
			t.Errorf("unexpected panel payload: %v", payload)
		}
		if payload["ssl"] != "Enable" || payload["sslType"] != "import-paste" {
			t.Errorf("unexpected SSL mode: %v", payload)
		}
		_, _ = w.Write([]byte(`{"code":200,"data":null}`))
	}))
	defer server.Close()

	cert := &model.Certificate{PrimaryDomain: "panel.example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "PRIVATE-KEY"}
	cfg := map[string]string{"panel_url": server.URL, "api_key": "test-key", "api_version": "v2"}
	if err := deployOnePanelPanel(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDeploySafelineMatchesExistingCert(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-SLCE-API-TOKEN") != "test-token" {
			t.Errorf("unexpected SafeLine token")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/api/open/cert" {
				t.Errorf("unexpected request: %s", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"err":null,"msg":"","data":{"nodes":[{"id":3,"domains":["example.com"]}]}}`))
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("invalid cert payload: %v", err)
			}
			if payload["id"] != float64(3) || payload["type"] != float64(2) {
				t.Errorf("unexpected cert payload: %v", payload)
			}
			manual, _ := payload["manual"].(map[string]any)
			if manual["crt"] != "FULLCHAIN" || manual["key"] != "PRIVATE-KEY" {
				t.Errorf("certificate was not submitted: %v", manual)
			}
			_, _ = w.Write([]byte(`{"err":null,"data":3}`))
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	cert := &model.Certificate{
		PrimaryDomain: "example.com",
		Domains:       []string{"example.com"},
		FullchainPEM:  "FULLCHAIN",
		KeyPEM:        "PRIVATE-KEY",
	}
	cfg := map[string]string{"panel_url": server.URL, "api_token": "test-token"}
	if err := deploySafeline(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDeploySafelineCreatesCertWhenNoMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"err":null,"data":{"nodes":[{"id":9,"domains":["other.example.com"]}]}}`))
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("invalid cert payload: %v", err)
			}
			if _, exists := payload["id"]; exists {
				t.Errorf("new certificate should not include an id: %v", payload)
			}
			_, _ = w.Write([]byte(`{"err":null,"data":10}`))
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	cert := &model.Certificate{
		PrimaryDomain: "example.com",
		Domains:       []string{"example.com"},
		FullchainPEM:  "FULLCHAIN",
		KeyPEM:        "PRIVATE-KEY",
	}
	cfg := map[string]string{"panel_url": server.URL, "api_token": "test-token"}
	if err := deploySafeline(cert, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestBaotaPluginDispatch(t *testing.T) {
	var sites, panels int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/site" && r.URL.Query().Get("action") == "SetSSL":
			sites++
		case r.URL.Path == "/config" && r.URL.Query().Get("action") == "SavePanelSSL":
			panels++
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"status":true,"msg":"ok"}`))
	}))
	defer server.Close()

	auth := map[string]string{"panel_url": server.URL, "api_secret": "secret"}
	config := map[string]string{"sites": "example.com"}

	if err := (baotaPlugin{}).Deploy(context.Background(), deployplugin.Request{Auth: auth, Config: config, Certificate: deployplugin.Certificate{
		Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "PRIVATE-KEY",
	}}); err != nil {
		t.Fatal(err)
	}
	if sites != 1 || panels != 0 {
		t.Fatalf("empty target should deploy to site: sites=%d panels=%d", sites, panels)
	}

	panelConfig := map[string]string{"target": "panel"}
	if err := (baotaPlugin{}).Deploy(context.Background(), deployplugin.Request{Auth: auth, Config: panelConfig, Certificate: deployplugin.Certificate{
		Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "PRIVATE-KEY",
	}}); err != nil {
		t.Fatal(err)
	}
	if sites != 1 || panels != 1 {
		t.Fatalf("panel target should deploy to panel: sites=%d panels=%d", sites, panels)
	}
}

func TestOnePanelPluginDispatch(t *testing.T) {
	var websites, panels int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/websites/search":
			_, _ = w.Write([]byte(`{"code":200,"data":{"total":1,"items":[{"id":7,"primaryDomain":"example.com","alias":"demo"}]}}`))
		case "/api/v2/websites/7/https":
			websites++
			_, _ = w.Write([]byte(`{"code":200,"data":null}`))
		case "/api/v2/core/settings/ssl/update":
			panels++
			_, _ = w.Write([]byte(`{"code":200,"data":null}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := deployplugin.Certificate{Domain: "example.com", FullchainPEM: "FULLCHAIN", KeyPEM: "PRIVATE-KEY"}
	auth := map[string]string{"panel_url": server.URL, "api_key": "test-key", "api_version": "v2"}

	if err := (onePanelPlugin{}).Deploy(context.Background(), deployplugin.Request{Auth: auth, Config: map[string]string{
		"website": "example.com",
	}, Certificate: cert}); err != nil {
		t.Fatal(err)
	}
	if websites != 1 || panels != 0 {
		t.Fatalf("empty target should deploy to website: websites=%d panels=%d", websites, panels)
	}

	if err := (onePanelPlugin{}).Deploy(context.Background(), deployplugin.Request{Auth: auth, Config: map[string]string{
		"target": "panel",
	}, Certificate: cert}); err != nil {
		t.Fatal(err)
	}
	if websites != 1 || panels != 1 {
		t.Fatalf("panel target should deploy to panel: websites=%d panels=%d", websites, panels)
	}
}
