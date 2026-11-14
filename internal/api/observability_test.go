package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func testAccessServer(t *testing.T, access bool, buffer *bytes.Buffer) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	server := &Server{
		AccessLog: access,
		Logger:    slog.New(slog.NewTextHandler(buffer, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	server.SetEntry("secret-entry")
	return server
}

func TestAccessLogRedactsEntryAndQuery(t *testing.T) {
	var buffer bytes.Buffer
	server := testAccessServer(t, true, &buffer)
	router := gin.New()
	router.Use(server.requestID(), server.accessLog(), server.recovery())
	router.GET("/:entry/api/ping", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/secret-entry/api/ping?token=super-secret", nil)
	request.Header.Set("X-Request-ID", "req-123")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	if got := recorder.Header().Get("X-Request-ID"); got != "req-123" {
		t.Fatalf("request id header not preserved: %q", got)
	}
	output := buffer.String()
	if strings.Contains(output, "secret-entry") || strings.Contains(output, "super-secret") {
		t.Fatalf("access log leaked sensitive value: %s", output)
	}
	if !strings.Contains(output, "path=/:entry/api/ping") {
		t.Fatalf("access log missing route template: %s", output)
	}
	if !strings.Contains(output, "request_id=req-123") {
		t.Fatalf("access log missing request id: %s", output)
	}
}

func TestAccessLogRedactsUnmatchedEntryPath(t *testing.T) {
	var buffer bytes.Buffer
	server := testAccessServer(t, true, &buffer)
	router := gin.New()
	router.Use(server.requestID(), server.accessLog(), server.recovery())
	router.NoRoute(func(c *gin.Context) {
		c.Status(http.StatusNotFound)
	})

	request := httptest.NewRequest(http.MethodGet, "/secret-entry/missing?key=value", nil)
	router.ServeHTTP(httptest.NewRecorder(), request)

	output := buffer.String()
	if strings.Contains(output, "secret-entry") || strings.Contains(output, "key=value") {
		t.Fatalf("access log leaked entry or query: %s", output)
	}
	if !strings.Contains(output, "path=/:entry/missing") {
		t.Fatalf("unmatched path not redacted: %s", output)
	}
}

func TestRecoveryLogsPanicAndReturnsJSON(t *testing.T) {
	var buffer bytes.Buffer
	server := testAccessServer(t, true, &buffer)
	router := gin.New()
	router.Use(server.requestID(), server.accessLog(), server.recovery())
	router.GET("/:entry/api/panic", func(c *gin.Context) {
		panic("boom")
	})

	request := httptest.NewRequest(http.MethodGet, "/secret-entry/api/panic", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"success":false`) {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
	output := buffer.String()
	if !strings.Contains(output, "http panic recovered") || !strings.Contains(output, "panic=boom") {
		t.Fatalf("panic not logged: %s", output)
	}
	if !strings.Contains(output, "status=500") {
		t.Fatalf("access log should record recovered status: %s", output)
	}
}

func TestAccessLogDisabledStillSetsRequestID(t *testing.T) {
	var buffer bytes.Buffer
	server := testAccessServer(t, false, &buffer)
	router := gin.New()
	router.Use(server.requestID(), server.accessLog())
	router.GET("/:entry/api/ping", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/secret-entry/api/ping", nil))

	if recorder.Header().Get("X-Request-ID") == "" {
		t.Fatal("request id should always be set")
	}
	if buffer.Len() != 0 {
		t.Fatalf("access log should be disabled: %s", buffer.String())
	}
}

func TestSanitizeRequestID(t *testing.T) {
	cases := map[string]string{
		"abc-123_4.5":           "abc-123_4.5",
		"":                      "",
		"bad value":             "",
		"bad\nvalue":            "",
		strings.Repeat("a", 65): "",
	}
	for input, expected := range cases {
		if got := sanitizeRequestID(input); got != expected {
			t.Fatalf("sanitizeRequestID(%q) = %q, want %q", input, got, expected)
		}
	}
}
