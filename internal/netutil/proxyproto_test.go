package netutil

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestReadProxyV1(t *testing.T) {
	trusted := mustTrusted(t, "10.0.0.2")
	reader := bufio.NewReader(bytes.NewBufferString("PROXY TCP4 203.0.113.9 10.0.0.2 12345 8080\r\nGET / HTTP/1.1\r\n"))
	remote, err := readProxyHeader(reader, netip.MustParseAddr("10.0.0.2"), trusted)
	if err != nil {
		t.Fatal(err)
	}
	if remote == nil || remote.String() != "203.0.113.9:12345" {
		t.Fatalf("unexpected remote addr: %v", remote)
	}
	rest, _ := reader.ReadString('\n')
	if rest != "GET / HTTP/1.1\r\n" {
		t.Fatalf("unexpected remaining data: %q", rest)
	}

	reader = bufio.NewReader(bytes.NewBufferString("PROXY UNKNOWN\r\n"))
	remote, err = readProxyHeader(reader, netip.MustParseAddr("10.0.0.2"), trusted)
	if err != nil || remote != nil {
		t.Fatalf("UNKNOWN 应保留 TCP peer: %v %v", remote, err)
	}

	reader = bufio.NewReader(bytes.NewBufferString("PROXY TCP4 203.0.113.9 10.0.0.2 12345 8080\r\n"))
	_, err = readProxyHeader(reader, netip.MustParseAddr("198.51.100.1"), trusted)
	if !errors.Is(err, ErrUntrustedProxyHeader) {
		t.Fatalf("非可信来源应拒绝: %v", err)
	}
}

func TestReadProxyV2(t *testing.T) {
	trusted := mustTrusted(t, "10.0.0.2")
	header := proxyV2Header(0x21, 0x11, [][]byte{
		{203, 0, 113, 9}, {10, 0, 0, 2}, {0x30, 0x39}, {0x1f, 0x90},
	})
	reader := bufio.NewReader(bytes.NewReader(header))
	remote, err := readProxyHeader(reader, netip.MustParseAddr("10.0.0.2"), trusted)
	if err != nil {
		t.Fatal(err)
	}
	if remote == nil || remote.String() != "203.0.113.9:12345" {
		t.Fatalf("unexpected remote addr: %v", remote)
	}

	ipv6 := netip.MustParseAddr("2001:db8::1").As16()
	dst := netip.MustParseAddr("2001:db8::2").As16()
	header = proxyV2Header(0x21, 0x21, [][]byte{
		ipv6[:], dst[:], {0x30, 0x39}, {0x1f, 0x90},
	})
	reader = bufio.NewReader(bytes.NewReader(header))
	remote, err = readProxyHeader(reader, netip.MustParseAddr("10.0.0.2"), trusted)
	if err != nil {
		t.Fatal(err)
	}
	if remote == nil || remote.String() != "[2001:db8::1]:12345" {
		t.Fatalf("unexpected IPv6 remote addr: %v", remote)
	}
}

func TestReadProxyHeaderIgnoresHTTP(t *testing.T) {
	reader := bufio.NewReader(bytes.NewBufferString("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	remote, err := readProxyHeader(reader, netip.MustParseAddr("10.0.0.2"), mustTrusted(t, "10.0.0.2"))
	if err != nil || remote != nil {
		t.Fatalf("普通 HTTP 不应被识别为 PROXY: %v %v", remote, err)
	}
}

func proxyV2Header(versionCommand, familyProtocol byte, parts [][]byte) []byte {
	payload := bytes.Join(parts, nil)
	header := append([]byte{}, proxyV2Signature...)
	header = append(header, versionCommand, familyProtocol, 0, 0)
	binary.BigEndian.PutUint16(header[len(header)-2:], uint16(len(payload)))
	return append(header, payload...)
}

func TestProxyProtoListenerRewritesRemoteAddr(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wrapped := NewProxyProtoListener(listener, mustTrusted(t, "127.0.0.1"))

	remote := make(chan net.Addr, 1)
	go func() {
		conn, err := wrapped.Accept()
		if err != nil {
			remote <- nil
			return
		}
		defer conn.Close()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(conn, buf); err != nil {
			remote <- nil
			return
		}
		remote <- conn.RemoteAddr()
	}()

	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := fmt.Fprint(client, "PROXY TCP4 203.0.113.9 127.0.0.1 12345 8080\r\nHELLO"); err != nil {
		t.Fatal(err)
	}

	select {
	case addr := <-remote:
		if addr == nil || addr.String() != "203.0.113.9:12345" {
			t.Fatalf("unexpected remote addr: %v", addr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for proxied connection")
	}
}

func TestProxyProtoConnRemoteAddr(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	conn := &proxyProtoConn{Conn: server}
	if conn.RemoteAddr() == nil {
		t.Fatal("RemoteAddr 不应为空")
	}
}
