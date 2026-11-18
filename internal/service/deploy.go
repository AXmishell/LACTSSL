package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/exec"
)

// Deploy 将证书部署到指定目标。deployConfig 由任务流程部署节点提供，
// 与凭据中的认证信息合并后交给具体部署实现。保留可变参数以兼容旧的调用方式。
func (m *Manager) Deploy(cert *model.Certificate, account *model.DeployAccount, deployConfigs ...map[string]string) error {
	return m.DeployContext(context.Background(), cert, account, deployConfigs...)
}

// DeployContext 与 Deploy 相同，但允许调用方传入请求/任务上下文，
// 便于服务关停或客户端断开时中止部署命令。
func (m *Manager) DeployContext(ctx context.Context, cert *model.Certificate, account *model.DeployAccount, deployConfigs ...map[string]string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if cert == nil || account == nil {
		return i18n.E("service.deploy.certificate_or_account_missing")
	}
	if strings.TrimSpace(cert.FullchainPEM) == "" {
		if strings.TrimSpace(cert.CertPEM) == "" {
			return i18n.E("service.deploy.certificate_empty")
		}
		cert.FullchainPEM = cert.CertPEM
	}
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
	// 旧 helper 可能注册了规范 ID，但不支持合并后新增的部署对象（例如
	// safeline 没有 panel/portal、baota 没有 docker）。此时按部署对象回退到旧 ID。
	if found {
		if key, value, ok := deployTargetModeForAccount(*account); ok && !deployTargetSupports(account.Type, key, value) {
			if legacy := legacyDeployerID(account.Type, deployConfig); legacy != "" {
				deployer, found = m.deployerFor(legacy)
			}
		}
	}
	if !found {
		// helper 仍是旧版本时，合并目标会以旧 ID 注册；按部署对象回退到旧 ID。
		if legacy := legacyDeployerID(account.Type, deployConfig); legacy != "" {
			deployer, found = m.deployerFor(legacy)
		}
	}
	if !found {
		return i18n.E("service.deploy.type_unsupported", "type", account.Type)
	}
	return deployer.Deploy(ctx, deployplugin.Request{
		AccountID:   account.ID,
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
		switch strings.TrimSpace(cfg["target"]) {
		case "panel":
			return "baota_panel"
		case "docker":
			return "baota_docker"
		default:
			return "baota_site"
		}
	case "1panel":
		if strings.TrimSpace(cfg["target"]) == "panel" {
			return "1panel_panel"
		}
		return "1panel_site"
	case "acepanel":
		if strings.TrimSpace(cfg["target"]) == "panel" {
			return "acepanel_panel"
		}
		return "acepanel"
	case "safeline":
		switch strings.TrimSpace(cfg["target"]) {
		case "panel":
			return "safeline_panel"
		case "portal":
			return "safeline_portal"
		default:
			return "safeline"
		}
	case "tencent":
		switch strings.TrimSpace(cfg["target"]) {
		case "cos":
			return "tencent_cos"
		case "teo":
			return "tencent_teo"
		case "waf":
			return "tencent_waf"
		default:
			return "tencent_cdn"
		}
	case "aliyun":
		switch strings.TrimSpace(cfg["target"]) {
		case "dcdn":
			return "aliyun_dcdn"
		case "esa":
			return "aliyun_esa"
		case "oss":
			return "aliyun_oss"
		case "waf":
			return "aliyun_waf"
		default:
			return "aliyun_cdn"
		}
	case "volcengine":
		if strings.TrimSpace(cfg["target"]) == "dcdn" {
			return "volcengine_dcdn"
		}
		return "volcengine_cdn"
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

// runCommand 在受限环境与进程组中执行一条 shell 命令。
// 超时或上下文取消时结束整个进程组，避免只杀掉 sh 而让 systemctl/docker
// 等子进程继续运行。
// commandOutputLimit 限制部署/重载命令保留的输出字节数，避免持续输出吃满内存。
const commandOutputLimit = 64 << 10

// tailBuffer 只保留最近 limit 字节；超出部分丢弃，不影响命令继续执行。
type tailBuffer struct {
	data  []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = append(b.data[:0], b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string { return string(b.data) }

func runCommand(ctx context.Context, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	// 子进程不继承完整环境，避免并发的 DNS 申请把 lego 注入的 API 密钥
	// （如 CLOUDFLARE_DNS_API_TOKEN）泄露给部署命令。
	cmd.Env = commandEnv()
	output := &tailBuffer{limit: commandOutputLimit}
	cmd.Stdout = output
	cmd.Stderr = output
	pluginexec.SetProcessGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			pluginexec.KillProcessGroup(cmd.Process.Pid)
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return i18n.E("service.deploy.command_timeout")
		}
		if ctx.Err() != nil {
			return i18n.E("service.deploy.command_canceled")
		}
		return i18n.E("service.deploy.command_failed", "error", err.Error(), "output", truncate(output.String(), 400))
	}
	return nil
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

func deployLocal(ctx context.Context, cert *model.Certificate, cfg map[string]string) error {
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
	if err := runCommand(ctx, cfg["reload_command"]); err != nil {
		return i18n.E("service.deploy.reload_failed", "error", err.Error())
	}
	return nil
}

func deploySSH(ctx context.Context, m *Manager, cert *model.Certificate, cfg map[string]string, account *model.DeployAccount) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return i18n.E("service.deploy.command_canceled")
	}
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
	// 首次连接默认拒绝未知主机密钥，必须显式开启 TOFU 或预先填写指纹，
	// 避免首次部署时被链路中间人窃取证书私钥与 SSH 凭据。
	allowTOFU := strings.EqualFold(strings.TrimSpace(cfg["trust_new_host_key"]), "true") ||
		strings.TrimSpace(cfg["trust_new_host_key"]) == "1"
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
			if !allowTOFU {
				return i18n.E("service.deploy.ssh_host_key_required", "actual", actual)
			}
			// 显式开启 TOFU 后记录指纹，后续连接必须一致。
			// 必须使用只更新 config 的写库方法，避免把账号名称/备注覆盖为空。
			if account == nil || account.ID <= 0 || m == nil || m.Store == nil {
				return i18n.E("service.deploy.ssh_host_key_save_failed", "error", "deploy account id is missing")
			}
			if account.Config == nil {
				account.Config = map[string]string{}
			}
			account.Config["host_key_fingerprint"] = actual
			if err := m.Store.UpdateDeployAccountConfig(account.ID, account.Config); err != nil {
				return i18n.E("service.deploy.ssh_host_key_save_failed", "error", err.Error())
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

	// stageFile 把内容写入目标目录下的唯一临时文件，并尽早收紧权限。
	// 私钥文件 chmod 失败必须中止，避免以 0644 短暂或永久落盘。
	stageFile := func(remote string, data []byte, mode os.FileMode) (string, error) {
		remote = strings.TrimSpace(remote)
		if remote == "" {
			return "", i18n.E("service.deploy.remote_path_required")
		}
		if err := sftpClient.MkdirAll(path.Dir(remote)); err != nil {
			return "", i18n.E("service.deploy.remote_mkdir_failed", "error", err.Error())
		}
		var suffix [6]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", i18n.E("service.deploy.remote_create_failed", "error", err.Error())
		}
		tmp := remote + ".lactssl.tmp." + hex.EncodeToString(suffix[:])
		f, err := sftpClient.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return "", i18n.E("service.deploy.remote_create_failed", "error", err.Error())
		}
		cleanup := func() {
			_ = f.Close()
			_ = sftpClient.Remove(tmp)
		}
		if err := sftpClient.Chmod(tmp, mode); err != nil {
			// 私钥（无组/其他权限）必须严格 0600；公钥证书允许部分服务忽略 chmod。
			if mode.Perm()&0o077 == 0 {
				cleanup()
				return "", i18n.E("service.deploy.remote_chmod_failed", "error", err.Error())
			}
		}
		if written, err := f.Write(data); err != nil || written != len(data) {
			cleanup()
			if err == nil {
				err = errors.New("short write")
			}
			return "", i18n.E("service.deploy.remote_write_failed", "error", err.Error())
		}
		if err := f.Sync(); err != nil {
			// 少数 SFTP 服务不支持 fsync；内容已 close 后落地，失败不阻断部署。
			_ = err
		}
		if err := f.Close(); err != nil {
			_ = sftpClient.Remove(tmp)
			return "", i18n.E("service.deploy.remote_write_failed", "error", err.Error())
		}
		return tmp, nil
	}

	certPath := strings.TrimSpace(cfg["cert_path"])
	keyPath := strings.TrimSpace(cfg["key_path"])
	if certPath == "" || keyPath == "" {
		return i18n.E("service.deploy.paths_required")
	}
	if certPath == keyPath {
		return i18n.E("service.deploy.paths_conflict")
	}
	certTmp, err := stageFile(certPath, []byte(cert.FullchainPEM), 0o644)
	if err != nil {
		return err
	}
	keyTmp, err := stageFile(keyPath, []byte(cert.KeyPEM), 0o600)
	if err != nil {
		_ = sftpClient.Remove(certTmp)
		return err
	}
	// 先把旧文件改名为备份，再依次替换私钥与证书；任一步失败都回滚双方。
	type replaceTarget struct {
		remote string
		tmp    string
		backup string
		hadOld bool
	}
	targets := []replaceTarget{{remote: keyPath, tmp: keyTmp}, {remote: certPath, tmp: certTmp}}
	// 备份阶段失败：只恢复已经改名的旧文件，并清理所有临时文件。
	restoreBackups := func(upto int) {
		for i := 0; i < upto; i++ {
			if targets[i].hadOld {
				_ = sftpClient.Rename(targets[i].backup, targets[i].remote)
			}
		}
		for i := range targets {
			_ = sftpClient.Remove(targets[i].tmp)
		}
	}
	// 替换阶段失败：恢复所有旧文件，并清理已写入的新文件与临时文件。
	rollbackAll := func() {
		for i := range targets {
			if targets[i].hadOld {
				_ = sftpClient.Remove(targets[i].remote)
				_ = sftpClient.Rename(targets[i].backup, targets[i].remote)
			} else {
				_ = sftpClient.Remove(targets[i].remote)
			}
			_ = sftpClient.Remove(targets[i].tmp)
		}
	}
	for i := range targets {
		if _, statErr := sftpClient.Lstat(targets[i].remote); statErr == nil {
			var suffix [6]byte
			if _, err := rand.Read(suffix[:]); err != nil {
				restoreBackups(i)
				return i18n.E("service.deploy.remote_rename_failed", "error", err.Error())
			}
			targets[i].backup = targets[i].remote + ".lactssl.bak." + hex.EncodeToString(suffix[:])
			if err := sftpClient.Rename(targets[i].remote, targets[i].backup); err != nil {
				restoreBackups(i)
				return i18n.E("service.deploy.remote_rename_failed", "error", err.Error())
			}
			targets[i].hadOld = true
		} else if !os.IsNotExist(statErr) {
			restoreBackups(i)
			return i18n.E("service.deploy.remote_rename_failed", "error", statErr.Error())
		}
	}
	for i := range targets {
		if err := sftpClient.Rename(targets[i].tmp, targets[i].remote); err != nil {
			rollbackAll()
			return i18n.E("service.deploy.remote_rename_failed", "error", err.Error())
		}
	}
	for _, target := range targets {
		if target.hadOld {
			_ = sftpClient.Remove(target.backup)
		}
	}

	if command := strings.TrimSpace(cfg["reload_command"]); command != "" {
		if err := ctx.Err(); err != nil {
			return i18n.E("service.deploy.command_canceled")
		}
		cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		session, err := client.NewSession()
		if err != nil {
			return i18n.E("service.deploy.ssh_session_failed", "error", err.Error())
		}
		defer session.Close()
		output := &tailBuffer{limit: commandOutputLimit}
		session.Stdout = output
		session.Stderr = output
		if err := session.Start(command); err != nil {
			return i18n.E("service.deploy.remote_command_failed", "error", err.Error(), "output", truncate(output.String(), 400))
		}
		done := make(chan error, 1)
		go func() { done <- session.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				return i18n.E("service.deploy.remote_command_failed", "error", err.Error(), "output", truncate(output.String(), 400))
			}
		case <-cmdCtx.Done():
			_ = session.Close()
			if ctx.Err() != nil {
				return i18n.E("service.deploy.command_canceled")
			}
			return i18n.E("service.deploy.command_timeout")
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
