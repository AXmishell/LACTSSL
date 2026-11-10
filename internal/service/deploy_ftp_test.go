package service

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"lactssl/internal/model"
)

// fakeFTPServer 只实现部署流程用到的最小子集，便于在没有外部依赖时测试上传。
type fakeFTPServer struct {
	ln    net.Listener
	mu    sync.Mutex
	files map[string][]byte
}

func newFakeFTPServer(t *testing.T) *fakeFTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeFTPServer{ln: ln, files: map[string][]byte{}}
	go server.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return server
}

func (s *fakeFTPServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeFTPServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	reply := func(message string) {
		_, _ = writer.WriteString(message + "\r\n")
		_ = writer.Flush()
	}

	reply("220 fake ftp ready")
	var dataListener net.Listener
	var renameFrom string
	defer func() {
		if dataListener != nil {
			_ = dataListener.Close()
		}
	}()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		command, argument := line, ""
		if index := strings.IndexByte(line, ' '); index >= 0 {
			command, argument = line[:index], line[index+1:]
		}

		switch strings.ToUpper(command) {
		case "USER":
			reply("331 password required")
		case "PASS":
			reply("230 logged in")
		case "FEAT":
			reply("500 command not understood")
		case "TYPE":
			reply("200 type set")
		case "PASV":
			if dataListener != nil {
				_ = dataListener.Close()
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				reply("425 cannot open data connection")
				continue
			}
			dataListener = listener
			tcpAddr := listener.Addr().(*net.TCPAddr)
			ip := strings.Split(tcpAddr.IP.String(), ".")
			port := tcpAddr.Port
			reply(fmt.Sprintf("227 Entering Passive Mode (%s,%s,%s,%s,%d,%d)", ip[0], ip[1], ip[2], ip[3], port/256, port%256))
		case "STOR":
			if dataListener == nil {
				reply("425 use PASV first")
				continue
			}
			reply("150 opening data connection")
			dataConn, err := dataListener.Accept()
			if err != nil {
				reply("425 data connection failed")
				continue
			}
			data, _ := io.ReadAll(dataConn)
			_ = dataConn.Close()
			_ = dataListener.Close()
			dataListener = nil
			s.mu.Lock()
			s.files[argument] = data
			s.mu.Unlock()
			reply("226 transfer complete")
		case "MKD":
			reply("257 \"/\" created")
		case "DELE":
			s.mu.Lock()
			delete(s.files, argument)
			s.mu.Unlock()
			reply("250 deleted")
		case "RNFR":
			renameFrom = argument
			reply("350 ready for RNTO")
		case "RNTO":
			s.mu.Lock()
			if data, ok := s.files[renameFrom]; ok {
				delete(s.files, renameFrom)
				s.files[argument] = data
			}
			s.mu.Unlock()
			reply("250 renamed")
		case "PWD":
			reply("257 \"/\"")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("500 command not understood")
		}
	}
}

func TestDeployFTP(t *testing.T) {
	server := newFakeFTPServer(t)
	host, port, err := net.SplitHostPort(server.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	cert := &model.Certificate{FullchainPEM: "FULLCHAIN", KeyPEM: "PRIVATE-KEY"}
	cfg := map[string]string{
		"host":      host,
		"port":      port,
		"user":      "deploy",
		"password":  "secret",
		"tls":       "none",
		"cert_path": "/etc/nginx/ssl/fullchain.pem",
		"key_path":  "/etc/nginx/ssl/private.key",
	}
	if err := deployFTP(cert, cfg); err != nil {
		t.Fatal(err)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.files) != 2 {
		t.Fatalf("expected 2 uploaded files, got %d: %v", len(server.files), server.files)
	}
	if got := string(server.files["/etc/nginx/ssl/fullchain.pem"]); got != "FULLCHAIN" {
		t.Fatalf("unexpected certificate content: %q", got)
	}
	if got := string(server.files["/etc/nginx/ssl/private.key"]); got != "PRIVATE-KEY" {
		t.Fatalf("unexpected private key content: %q", got)
	}
}

func TestDeployFTPValidation(t *testing.T) {
	cert := &model.Certificate{FullchainPEM: "FULLCHAIN", KeyPEM: "PRIVATE-KEY"}
	cases := []map[string]string{
		{"user": "deploy", "password": "secret"},
		{"host": "127.0.0.1", "password": "secret"},
		{"host": "127.0.0.1", "user": "deploy"},
		{"host": "127.0.0.1", "user": "deploy", "password": "secret", "port": "abc"},
		{"host": "127.0.0.1", "user": "deploy", "password": "secret", "tls": "unknown"},
	}
	for index, cfg := range cases {
		if err := deployFTP(cert, cfg); err == nil {
			t.Fatalf("case %d: expected validation error", index)
		}
	}
}
