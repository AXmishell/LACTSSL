package btwaf

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lactssl/internal/plugin/impl/deploy"
)

func TestDeployBtWAF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("waf_request_time") == "" || r.Header.Get("waf_request_token") == "" {
			t.Errorf("missing waf auth headers")
		}
		switch r.URL.Path {
		case "/api/wafmastersite/get_site_list":
			_, _ = w.Write([]byte(`{"code":0,"res":{"list":[{"site_id":"site-1","site_name":"demo"}]}}`))
		case "/api/wafmastersite/modify_site":
			var payload struct {
				SiteID string `json:"site_id"`
				Server struct {
					SSL struct {
						FullChain  string `json:"full_chain"`
						PrivateKey string `json:"private_key"`
					} `json:"ssl"`
				} `json:"server"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.SiteID != "site-1" || payload.Server.SSL.FullChain != "FULLCHAIN" || payload.Server.SSL.PrivateKey != "KEY" {
				t.Errorf("unexpected payload: %+v", payload)
			}
			_, _ = w.Write([]byte(`{"code":0}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cert := &deploy.Certificate{FullchainPEM: "FULLCHAIN", KeyPEM: "KEY"}
	if err := deployBtWAF(cert, map[string]string{"url": server.URL, "api_key": "key", "sites": "demo"}); err != nil {
		t.Fatal(err)
	}
}

func TestToken(t *testing.T) {
	if token("1700000000", "secret") != token("1700000000", "secret") {
		t.Fatal("token should be deterministic")
	}
}
