package acme

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/go-acme/lego/v4/challenge/tlsalpn01"
)

func startTLSALPNChallengeServer(t *testing.T, domain, keyAuth string) int {
	t.Helper()
	cert, err := tlsalpn01.ChallengeCert(domain, keyAuth)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{*cert},
		NextProtos:   []string{acmeTLSALPNProtocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.(*tls.Conn).Handshake()
			}()
		}
	}()
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestTLSALPN01ValidateSuccess(t *testing.T) {
	keyAuth := "token-123.thumbprint"
	port := startTLSALPNChallengeServer(t, "localhost", keyAuth)
	v := NewTLSALPN01Validator()
	v.Port = port
	v.AllowedCIDRs = []string{"127.0.0.0/8", "::1/128"}
	if err := v.Validate(context.Background(), "localhost", keyAuth); err != nil {
		t.Fatalf("TLS-ALPN-01 验证失败: %v", err)
	}
}

func TestTLSALPN01ValidateWrongKeyAuth(t *testing.T) {
	port := startTLSALPNChallengeServer(t, "localhost", "token-123.thumbprint")
	v := NewTLSALPN01Validator()
	v.Port = port
	v.AllowedCIDRs = []string{"127.0.0.0/8", "::1/128"}
	err := v.Validate(context.Background(), "localhost", "token-123.other")
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("错误的 keyAuthorization 应验证失败: %v", err)
	}
}
