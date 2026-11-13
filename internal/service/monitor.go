package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/url"
	"strings"
	"time"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// CheckMonitor 检测一个站点证书并更新状态。
func (m *Manager) CheckMonitor(monitorID int64) error {
	monitor, err := m.Store.GetMonitor(monitorID)
	if err != nil {
		return i18n.E("monitor.error.not_found", "error", err.Error())
	}

	now := time.Now().UTC()
	interval := monitor.IntervalHour
	if interval <= 0 {
		interval = 24
	}
	next := now.Add(time.Duration(interval) * time.Hour)

	cert, chain, checkErr := fetchCertificate(monitor.Target)
	status := "valid"
	message := ""
	issuer := ""
	var expires *time.Time

	if checkErr != nil {
		status = "error"
		message = i18n.EncodeError(checkErr)
		next = now.Add(time.Hour)
	} else {
		issuer = cert.Issuer.CommonName
		if issuer == "" && len(cert.Issuer.Organization) > 0 {
			issuer = cert.Issuer.Organization[0]
		}
		expiry := cert.NotAfter
		expires = &expiry
		days := int(time.Until(expiry).Hours() / 24)
		if now.After(expiry) {
			status = "expired"
			message = i18n.Encode("monitor.message.expired", map[string]any{"date": expiry.Local().Format("2006-01-02")})
		} else if now.Before(cert.NotBefore) {
			status = "error"
			message = i18n.Encode("monitor.message.not_yet_valid", map[string]any{"time": cert.NotBefore.Local().Format("2006-01-02 15:04")})
		} else if days <= 30 {
			status = "expiring"
			message = i18n.Encode("monitor.message.expiring", map[string]any{"days": days})
		} else {
			message = i18n.Encode("monitor.message.valid", map[string]any{"days": days})
		}
		host, _, _ := parseTarget(monitor.Target)
		if host != "" {
			if err := cert.VerifyHostname(host); err != nil {
				status = "error"
				message = i18n.Encode("monitor.message.hostname_failed", map[string]any{
					"message": i18n.Render(m.Locale(), message),
					"error":   err.Error(),
				})
			} else if status != "expired" && status != "error" {
				// 除了主机名匹配，还必须校验证书链是否受信任，避免链路上的中间人
				// 用自签证书伪造“正常”状态、掩盖真实证书问题。
				if err := verifyPeerChain(cert, chain, host); err != nil {
					status = "error"
					message = i18n.Encode("monitor.message.trust_failed", map[string]any{
						"message": i18n.Render(m.Locale(), message),
						"error":   err.Error(),
					})
				}
			}
		}
	}

	if err := m.Store.UpdateMonitorResult(monitor.ID, status, message, issuer, expires, &now, &next); err != nil {
		return err
	}
	level := "info"
	if status == "error" || status == "expired" || status == "expiring" {
		level = "warning"
	}
	m.Store.AddLogKey("monitor", monitor.ID, level, "log.monitor.result", map[string]any{
		"name": monitor.Name, "target": monitor.Target, "message": i18n.Render(m.Locale(), message),
	})

	if monitor.Status != status && (status == "expiring" || status == "expired" || status == "error") {
		m.notifyMonitor(monitor, status, message, expires)
	}
	return checkErr
}

func fetchCertificate(target string) (*x509.Certificate, []*x509.Certificate, error) {
	host, port, err := parseTarget(target)
	if err != nil {
		return nil, nil, err
	}
	if host == "" {
		return nil, nil, i18n.E("monitor.error.target_unresolvable")
	}
	addr := net.JoinHostPort(host, port)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := dialSafe(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, i18n.E("monitor.error.connect_failed", "address", addr, "error", SafeError(err))
	}
	conn := tls.Client(raw, &tls.Config{
		ServerName: host,
		// 这里仍跳过握手阶段的链校验，以便读取已过期/自签证书的到期时间；
		// 调用方随后会单独执行 verifyPeerChain 并报告不受信任状态。
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, nil, i18n.E("monitor.error.connect_failed", "address", addr, "error", SafeError(err))
	}
	defer conn.Close()
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, nil, i18n.E("monitor.error.no_cert")
	}
	return state.PeerCertificates[0], state.PeerCertificates[1:], nil
}

// verifyPeerChain 使用系统根证书校验目标证书链，单独报告不受信任/链不完整问题。
func verifyPeerChain(cert *x509.Certificate, chain []*x509.Certificate, host string) error {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	intermediates := x509.NewCertPool()
	for _, item := range chain {
		intermediates.AddCert(item)
	}
	_, err = cert.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		DNSName:       host,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err
}

func parseTarget(target string) (string, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", i18n.E("monitor.error.target_empty")
	}
	if strings.Contains(target, "://") {
		u, err := url.Parse(target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return "", "", i18n.E("monitor.error.target_invalid")
		}
		port := u.Port()
		if port == "" {
			port = "443"
		}
		return u.Hostname(), port, nil
	}
	if host, port, err := net.SplitHostPort(target); err == nil {
		return host, port, nil
	}
	return target, "443", nil
}

func (m *Manager) notifyMonitor(monitor *model.Monitor, status, message string, expires *time.Time) {
	current := m.Locale()
	subject := i18n.T(current, "email.monitor.subject", map[string]any{"name": monitor.Name})
	expiry := i18n.T(current, "common.unknown", nil)
	if expires != nil {
		expiry = expires.Local().Format("2006-01-02 15:04:05")
	}
	body := i18n.Th(current, "email.monitor.body", map[string]any{
		"name":    monitor.Name,
		"target":  monitor.Target,
		"status":  status,
		"expires": expiry,
		"message": i18n.Render(current, message),
	})
	m.sendNotification(subject, body)
}
