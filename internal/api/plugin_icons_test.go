package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestListPluginIconsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dataDir := t.TempDir()
	s := &Server{DataDir: dataDir}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	s.listPluginIcons(ctx)
	if recorder.Code != 200 {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Header().Get("Cache-Control") == "" {
		t.Fatal("missing cache header")
	}
	var body struct {
		Success bool                         `json:"success"`
		Data    map[string]map[string]string `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}
