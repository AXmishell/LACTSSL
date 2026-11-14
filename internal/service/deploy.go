package service

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"lactssl/internal/deployplugin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// Deploy 将证书部署到指定目标。deployConfig 由任务流程部署节点提供，
// 与凭据中的认证信息合并后交给具体部署实现。保留可变参数以兼容旧的调用方式。
func (m *Manager) Deploy(cert *model.Certificate, account *model.DeployAccount, deployConfigs ...map[string]string) error {
	if strings.TrimSpace(cert.KeyPEM) == "" {
		return i18n.E("service.deploy.private_key_missing")
	}
	var deployConfig map[string]string
	if len(deployConfigs) > 0 {
		deployConfig = deployConfigs[0]
	}
	// 已合并目标的部署对象可能只记录在凭据的兼容字段中。旧客户端或手动
	// 部署没有传 domain_type/target 时补上，避免误按默认 CDN/网站部署。
	deployConfig = deployModeFallback(account, deployConfig)
	deployer, found := m.deployerFor(account.Type)
	if !found {
		// helper 仍是旧版本时，合并目标会以旧 ID 注册；按部署对象回退到旧 ID。
		if legacy := legacyDeployerID(account.Type, deployConfig); legacy != "" {
			deployer, found = m.deployerFor(legacy)
		}
	}
	if !found {
		return i18n.E("service.deploy.type_unsupported", "type", account.Type)
	}
	return deployer.Deploy(context.Background(), deployplugin.Request{
		Auth:        account.Config,
		Config:      deployConfig,
		Certificate: pluginCertificate(cert),
	})
}

// legacyDeployerID 把合并目标映射回旧部署目标 ID，用于旧 helper 兼容。
func legacyDeployerID(deployType string, cfg map[string]string) string {
	switch strings.ToLower(strings.TrimSpace(deployType)) {
	case "qiniu":
		if strings.TrimSpace(cfg["domain_type"]) == "kodo" {
			return "qiniu_kodo"
		}
		return "qiniu_cdn"
	case "baota":
		if strings.TrimSpace(cfg["target"]) == "panel" {
			return "baota_panel"
		}
		return "baota_site"
	case "1panel":
		if strings.TrimSpace(cfg["target"]) == "panel" {
			return "1panel_panel"
		}
		return "1panel_site"
	default:
		return ""
	}
}

// deployModeFallback 为已合并目标补全部署对象。
func deployModeFallback(account *model.DeployAccount, cfg map[string]string) map[string]string {
	if account == nil {
		return cfg
	}
	key, value, ok := deployTargetModeForAccount(*account)
	if !ok {
		return cfg
	}
	if strings.TrimSpace(cfg[key]) != "" {
		return cfg
	}
	out := make(map[string]string, len(cfg)+1)
	for k, v := range cfg {
		out[k] = v
	}
	out[key] = value
	return out
}

// writeFileAtomic 先写临时文件再重命名，避免服务读到半个文件。
func writeFileAtomic(target string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return i18n.E("service.deploy.mkdir_failed", "dir", dir, "error", err.Error())
	}
	tmp, err := os.CreateTemp(dir, ".lactssl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	return nil
}

func runCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	cmd := exec.Command("sh", "-c", command)
	// 子进程不继承完整环境，避免并发的 DNS 申请把 lego 注入的 API 密钥
	// （如 CLOUDFLARE_DNS_API_TOKEN）泄露给部署命令。
	cmd.Env = commandEnv()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return i18n.E("service.deploy.command_failed", "error", err.Error(), "output", truncate(output.String(), 400))
		}
		return nil
	case <-time.After(2 * time.Minute):
		_ = cmd.Process.Kill()
		return i18n.E("service.deploy.command_timeout")
	}
}

// commandEnv 返回部署/重载命令允许继承的环境变量。
// 只保留运行命令必需的基础变量与常见代理/工具配置，避免把应用密钥传给子进程。
func commandEnv() []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
		"LANG": true, "TZ": true, "TMPDIR": true, "TMP": true, "TEMP": true,
		"TERM": true, "PWD": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
		"http_proxy": true, "https_proxy": true, "no_proxy": true,
		"KUBECONFIG": true, "DOCKER_HOST": true, "XDG_RUNTIME_DIR": true,
	}
	var env []string
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if allowed[name] || strings.HasPrefix(name, "LC_") {
			env = append(env, item)
		}
	}
	return env
}

func deployLocal(cert *model.Certificate, cfg map[string]string) error {
	certPath := strings.TrimSpace(cfg["cert_path"])
	keyPath := strings.TrimSpace(cfg["key_path"])
	if certPath == "" || keyPath == "" {
		return i18n.E("service.deploy.paths_required")
	}
	if err := writeFileAtomic(certPath, []byte(cert.FullchainPEM), 0o644); err != nil {
		return i18n.E("service.deploy.write_cert_failed", "error", err.Error())
	}
	if err := writeFileAtomic(keyPath, []byte(cert.KeyPEM), 0o600); err != nil {
		return i18n.E("service.deploy.write_key_failed", "error", err.Error())
	}
	if err := runCommand(cfg["reload_command"]); err != nil {
		return i18n.E("service.deploy.reload_failed", "error", err.Error())
	}
	return nil
}

func deploySSH(m *Manager, cert *model.Certificate, cfg map[string]string, account *model.DeployAccount) error {
	host := strings.TrimSpace(cfg["host"])
	user := strings.TrimSpace(cfg["user"])
	if host == "" || user == "" {
		return i18n.E("service.deploy.ssh_host_user_required")
	}
	port := strings.TrimSpace(cfg["port"])
	if port == "" {
		port = "22"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return i18n.E("service.deploy.ssh_port_invalid")
	}

	authMethods := []ssh.AuthMethod{}
	if cfg["auth_type"] == "key" && strings.TrimSpace(cfg["private_key"]) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg["private_key"]))
		if err != nil {
			return i18n.E("service.deploy.ssh_key_parse_failed", "error", err.Error())
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	} else {
		if cfg["password"] == "" {
			return i18n.E("service.deploy.ssh_password_required")
		}
		authMethods = append(authMethods, ssh.Password(cfg["password"]))
	}

	pinned := strings.TrimSpace(cfg["host_key_fingerprint"])
	sshConfig := &ssh.ClientConfig{
		User:    user,
		Auth:    authMethods,
		Timeout: 15 * time.Second,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			actual := ssh.FingerprintSHA256(key)
			if pinned != "" {
				if actual != pinned {
					return i18n.E("service.deploy.ssh_host_key_mismatch", "expected", pinned, "actual", actual)
				}
				return nil
			}
			// 首次连接采用 TOFU：记录指纹，后续连接必须一致。
			if account != nil && account.ID > 0 && m != nil && m.Store != nil {
				if account.Config == nil {
					account.Config = map[string]string{}
				}
				account.Config["host_key_fingerprint"] = actual
				if err := m.Store.UpdateDeployAccount(account); err != nil {
					return i18n.E("service.deploy.ssh_host_key_save_failed", "error", err.Error())
				}
			}
			return nil
		},
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(host, port), sshConfig)
	if err != nil {
		return i18n.E("service.deploy.ssh_connect_failed", "error", err.Error())
	}
	defer client.Close()

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return i18n.E("service.deploy.sftp_session_failed", "error", err.Error())
	}
	defer sftpClient.Close()

	upload := func(remote string, data []byte, mode os.FileMode) error {
		remote = strings.TrimSpace(remote)
		if remote == "" {
			return i18n.E("service.deploy.remote_path_required")
		}
		if err := sftpClient.MkdirAll(path.Dir(remote)); err != nil {
			return i18n.E("service.deploy.remote_mkdir_failed", "error", err.Error())
		}
		tmp := remote + ".lactssl.tmp"
		f, err := sftpClient.Create(tmp)
		if err != nil {
			return i18n.E("service.deploy.remote_create_failed", "error", err.Error())
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := sftpClient.Chmod(tmp, mode); err != nil {
			// 部分 SFTP 服务不支持 chmod，忽略即可。
			_ = err
		}
		_ = sftpClient.Remove(remote)
		if err := sftpClient.Rename(tmp, remote); err != nil {
			return i18n.E("service.deploy.remote_rename_failed", "error", err.Error())
		}
		return nil
	}

	if err := upload(cfg["cert_path"], []byte(cert.FullchainPEM), 0o644); err != nil {
		return err
	}
	if err := upload(cfg["key_path"], []byte(cert.KeyPEM), 0o600); err != nil {
		return err
	}

	if command := strings.TrimSpace(cfg["reload_command"]); command != "" {
		session, err := client.NewSession()
		if err != nil {
			return i18n.E("service.deploy.ssh_session_failed", "error", err.Error())
		}
		defer session.Close()
		var output bytes.Buffer
		session.Stdout = &output
		session.Stderr = &output
		if err := session.Run(command); err != nil {
			return i18n.E("service.deploy.remote_command_failed", "error", err.Error(), "output", truncate(output.String(), 400))
		}
	}
	return nil
}

func splitConfigList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "..."
}
