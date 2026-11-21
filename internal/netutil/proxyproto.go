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
	"strconv"
	"strings"
	"time"
)

var (
	proxyV2Signature = []byte{0x0d, 0x0a, 0x0d, 0x0a, 0x00, 0x0d, 0x0a, 0x51, 0x55, 0x49, 0x54, 0x0a}
	// ErrUntrustedProxyHeader 表示非可信对端发送了 PROXY protocol 头。
	ErrUntrustedProxyHeader = errors.New("proxy protocol header from untrusted peer")
)

const (
	proxyHeaderTimeout = 2 * time.Second
	maxProxyV1Line     = 108
)

// ProxyProtoListener 在接受连接时读取 PROXY protocol v1/v2 头。
// 只有 TCP 对端在 trusted 集合内时，头中的源地址才会覆盖 RemoteAddr。
type ProxyProtoListener struct {
	net.Listener
	trusted TrustedSet
}

// NewProxyProtoListener 包装监听器；trusted 为空时所有 PROXY 头都会被拒绝。
func NewProxyProtoListener(listener net.Listener, trusted TrustedSet) net.Listener {
	if listener == nil {
		return nil
	}
	return &ProxyProtoListener{Listener: listener, trusted: trusted}
}

func (l *ProxyProtoListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		peer, _ := ParsePeer(conn.RemoteAddr().String())
		reader := bufio.NewReader(conn)
		_ = conn.SetReadDeadline(time.Now().Add(proxyHeaderTimeout))
		remote, err := readProxyHeader(reader, peer, l.trusted)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			_ = conn.Close()
			// 非可信来源或畸形头不应终止整个监听，只丢弃当前连接。
			continue
		}
		return &proxyProtoConn{Conn: conn, reader: reader, remote: remote}, nil
	}
}

type proxyProtoConn struct {
	net.Conn
	reader *bufio.Reader
	remote net.Addr
}

func (c *proxyProtoConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *proxyProtoConn) RemoteAddr() net.Addr {
	if c.remote != nil {
		return c.remote
	}
	return c.Conn.RemoteAddr()
}

// readProxyHeader 在连接开头尝试读取 PROXY v1/v2 头。
// 返回 nil 表示没有头（或超时且未读到任何字节），连接按普通 HTTP 处理。
func readProxyHeader(reader *bufio.Reader, peer netip.Addr, trusted TrustedSet) (net.Addr, error) {
	signature, err := reader.Peek(len(proxyV2Signature))
	if err != nil {
		if len(signature) == 0 && isTimeout(err) {
			return nil, nil
		}
		if len(signature) == 0 && errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		// 不足以判断签名时，交给 HTTP 层处理；不消费任何字节。
		if len(signature) > 0 {
			return nil, err
		}
		return nil, err
	}
	if bytes.Equal(signature, proxyV2Signature) {
		if !trusted.Contains(peer) {
			return nil, ErrUntrustedProxyHeader
		}
		return readProxyV2(reader)
	}
	if bytes.HasPrefix(signature, []byte("PROXY ")) {
		if !trusted.Contains(peer) {
			return nil, ErrUntrustedProxyHeader
		}
		return readProxyV1(reader)
	}
	return nil, nil
}

func readProxyV1(reader *bufio.Reader) (net.Addr, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > maxProxyV1Line {
		return nil, fmt.Errorf("PROXY v1 header too long")
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	fields := strings.Fields(line)
	if len(fields) == 2 && fields[1] == "UNKNOWN" {
		return nil, nil
	}
	if len(fields) != 6 || fields[0] != "PROXY" {
		return nil, fmt.Errorf("invalid PROXY v1 header")
	}
	switch fields[1] {
	case "TCP4", "TCP6":
	default:
		return nil, fmt.Errorf("unsupported PROXY v1 protocol %q", fields[1])
	}
	source, ok := ParseHeaderIP(fields[2])
	if !ok {
		return nil, fmt.Errorf("invalid PROXY v1 source address")
	}
	port, err := strconv.Atoi(fields[4])
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid PROXY v1 source port")
	}
	return &net.TCPAddr{IP: net.IP(source.AsSlice()), Port: port}, nil
}

func readProxyV2(reader *bufio.Reader) (net.Addr, error) {
	header := make([]byte, 16)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	versionCommand := header[12]
	if versionCommand>>4 != 2 {
		return nil, fmt.Errorf("invalid PROXY v2 version")
	}
	command := versionCommand & 0x0f
	length := int(binary.BigEndian.Uint16(header[14:16]))
	if length > 4096 {
		return nil, fmt.Errorf("PROXY v2 payload too large")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	if command == 0x00 { // LOCAL：健康检查等，不携带真实源地址
		return nil, nil
	}
	if command != 0x01 { // PROXY
		return nil, fmt.Errorf("invalid PROXY v2 command")
	}
	if header[13]&0x0f != 0x01 { // 只接受 STREAM
		return nil, fmt.Errorf("unsupported PROXY v2 transport")
	}
	family := header[13] >> 4
	switch family {
	case 0x1: // AF_INET
		if len(payload) < 12 {
			return nil, fmt.Errorf("short PROXY v2 IPv4 payload")
		}
		ip := netip.AddrFrom4([4]byte(payload[0:4]))
		port := int(binary.BigEndian.Uint16(payload[8:10]))
		return &net.TCPAddr{IP: net.IP(ip.AsSlice()), Port: port}, nil
	case 0x2: // AF_INET6
		if len(payload) < 36 {
			return nil, fmt.Errorf("short PROXY v2 IPv6 payload")
		}
		var raw [16]byte
		copy(raw[:], payload[0:16])
		ip := netip.AddrFrom16(raw).Unmap()
		port := int(binary.BigEndian.Uint16(payload[32:34]))
		return &net.TCPAddr{IP: net.IP(ip.AsSlice()), Port: port}, nil
	default:
		return nil, nil
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
