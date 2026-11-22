package api

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
	"lactssl/internal/ratelimit"
	"lactssl/internal/service"
	"lactssl/internal/util"
)

func (s *Server) setting(key, def string) string {
	v, _ := s.Store.GetSetting(key)
	if v == "" {
		return def
	}
	return v
}

func (s *Server) handleMeta(c *gin.Context) {
	current := locale(c)
	privateCAs := make([]gin.H, 0)
	if list, err := s.Store.ListPrivateCAs(); err == nil {
		now := time.Now()
		for _, ca := range list {
			if !now.Before(ca.NotAfter) {
				continue
			}
			label := ca.Name
			if ca.CommonName != "" && ca.CommonName != ca.Name {
				label = ca.Name + " · " + ca.CommonName
			}
			privateCAs = append(privateCAs, gin.H{
				"value":     service.PrivateCAValue(ca.ID),
				"label":     label,
				"ca_type":   ca.CAType,
				"path_len":  ca.PathLen,
				"not_after": ca.NotAfter,
			})
		}
	}
	caPools, _ := s.Store.ListCAPoolSummaries()
	ok(c, gin.H{
		"version":       Version,
		"language":      string(current),
		"languages":     supportedLanguages(),
		"cas":           service.LocalizedCADefs(s.Store, current),
		"ca_pools":      caPools,
		"private_cas":   privateCAs,
		"dns_providers": service.LocalizeDNSProviders(current),
		"deploy_types":  service.LocalizeDeployTypes(current),
		"challenges": []gin.H{
			{"value": "dns01", "label": i18n.T(current, "meta.challenge.dns01.label", nil), "desc": i18n.T(current, "meta.challenge.dns01.desc", nil)},
		},
		"key_types": []gin.H{
			{"value": "ecdsa", "label": i18n.T(current, "meta.key.ecdsa.label", nil)},
			{"value": "ecdsa384", "label": i18n.T(current, "meta.key.ecdsa384.label", nil)},
			{"value": "rsa2048", "label": i18n.T(current, "meta.key.rsa2048.label", nil)},
			{"value": "rsa4096", "label": i18n.T(current, "meta.key.rsa4096.label", nil)},
		},
	})
}

// supportedLanguages 返回可切换语言列表，新增语言文件后会自动出现。
func supportedLanguages() []gin.H {
	list := make([]gin.H, 0)
	for _, value := range i18n.Supported() {
		list = append(list, gin.H{
			"value": string(value),
			"label": i18n.T(value, "language."+string(value), nil),
		})
	}
	return list
}

func (s *Server) handleDashboard(c *gin.Context) {
	data, err := s.Store.Dashboard()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	current := locale(c)
	bucketKeys := []string{
		"dashboard.bucket.expired",
		"dashboard.bucket.7",
		"dashboard.bucket.30",
		"dashboard.bucket.90",
		"dashboard.bucket.90plus",
	}
	for index := range data.ExpiryBuckets {
		if index < len(bucketKeys) {
			data.ExpiryBuckets[index].Label = i18n.T(current, bucketKeys[index], nil)
		}
	}
	data.RecentTasks = localizeTasks(c, data.RecentTasks)
	data.RecentLogs = localizeLogs(c, data.RecentLogs)
	// 仪表盘同时显示 LACTSSL 进程的本地时间，便于与证书生效/到期时间对照。
	now := time.Now()
	_, offset := now.Zone()
	data.ServerTime = now
	data.ServerTimezone = now.Format("MST")
	data.ServerOffsetSeconds = offset
	ok(c, data)
}

func (s *Server) getSettings(c *gin.Context) {
	smtpPassword, _ := s.Store.GetSecret("smtp.password")
	user := getUser(c)
	passkeyCount, _ := s.Store.CountPasskeys(user.ID)
	resp := gin.H{
		"entry":                 s.Entry(),
		"passkey_enabled":       passkeyCount > 0,
		"passkey_count":         passkeyCount,
		"language":              s.setting("language", string(i18n.Default)),
		"acme_email":            s.setting("acme.email", ""),
		"acme_ca":               s.setting("acme.ca", "letsencrypt"),
		"acme_ca_pool_id":       s.settingInt(service.CAPoolSettingKey, 0),
		"acme_key_type":         s.setting("acme.key_type", "ecdsa"),
		"acme_renew_days":       s.settingInt("acme.renew_days", 30),
		"csr_allowed_domains":   s.setting("csr.allowed_domains", ""),
		"csr_allow_all_domains": s.setting("csr.allow_all_domains", "false") == "true",
		"smtp_host":             s.setting("smtp.host", ""),
		"smtp_port":             s.settingInt("smtp.port", 465),
		"smtp_username":         s.setting("smtp.username", ""),
		"smtp_from":             s.setting("smtp.from", ""),
		"smtp_ssl":              s.setting("smtp.ssl", "true") == "true",
		"smtp_password_set":     smtpPassword != "",
		"notify_enabled":        s.setting("notify.enabled", "false") == "true",
		"notify_to":             s.setting("notify.to", ""),
		"notify_channels":       s.Manager.NotifyChannelsForUI(),
		"notify_channel_defs":   s.Manager.NotifyChannelDefinitions(),
		"security_notify":       s.Manager.LoadSecurityNotifyPolicy(user.ID),
	}
	// 门禁入口只返回给浏览器会话；API 密钥不应借此获知入口。
	if apiKeyFromContext(c) != nil {
		delete(resp, "entry")
	}
	ok(c, resp)
}

func (s *Server) settingInt(key string, def int) int {
	v, _ := strconv.Atoi(s.setting(key, ""))
	if v == 0 {
		return def
	}
	return v
}

func (s *Server) updateSettings(c *gin.Context) {
	var body struct {
		Language           *string                                `json:"language"`
		ACMEEmail          *string                                `json:"acme_email"`
		ACMECA             *string                                `json:"acme_ca"`
		ACMECAPoolID       *int64                                 `json:"acme_ca_pool_id"`
		ACMEKeyType        *string                                `json:"acme_key_type"`
		ACMERenew          *int                                   `json:"acme_renew_days"`
		CSRAllowedDomains  *string                                `json:"csr_allowed_domains"`
		CSRAllowAllDomains *bool                                  `json:"csr_allow_all_domains"`
		SMTPHost           *string                                `json:"smtp_host"`
		SMTPPort           *int                                   `json:"smtp_port"`
		SMTPUsername       *string                                `json:"smtp_username"`
		SMTPPassword       *string                                `json:"smtp_password"`
		SMTPFrom           *string                                `json:"smtp_from"`
		SMTPSSL            *bool                                  `json:"smtp_ssl"`
		NotifyEnable       *bool                                  `json:"notify_enabled"`
		NotifyTo           *string                                `json:"notify_to"`
		NotifyChannels     map[string]service.NotifyChannelConfig `json:"notify_channels"`
		SecurityNotify     *service.SecurityNotifyPolicy          `json:"security_notify"`
		// 出站目标（SMTP/通知）可能携带已存储的密钥，变更时必须再认证。
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	// CSR 白名单与默认 CA/调度组属于证书签发策略，而不是普通外观设置。
	// API Key 必须显式持有 security:write 才能修改；浏览器会话仍由 CSRF/登录态保护。
	sensitivePolicy := body.ACMECA != nil || body.ACMECAPoolID != nil ||
		body.CSRAllowedDomains != nil || body.CSRAllowAllDomains != nil
	if sensitivePolicy && !s.requireAPIKeyScope(c, apiScopeSecurityWrite) {
		return
	}
	if body.SMTPHost != nil || body.SMTPPort != nil || body.SMTPUsername != nil ||
		body.SMTPPassword != nil || body.SMTPFrom != nil || body.SMTPSSL != nil ||
		body.NotifyEnable != nil || body.NotifyTo != nil || body.NotifyChannels != nil ||
		body.SecurityNotify != nil {
		// 修改 SMTP/通知目标/安全提醒策略等于改变敏感凭据或告警的去向，必须证明当前身份。
		if !s.requireReauth(c, getUser(c), body.Password, body.TOTP) {
			return
		}
	}
	if body.SecurityNotify != nil {
		if err := service.ValidateSecurityNotifyPolicy(*body.SecurityNotify); err != nil {
			failErr(c, http.StatusBadRequest, err)
			return
		}
	}
	user := getUser(c)
	oldPolicy := service.DefaultSecurityNotifyPolicy()
	oldSMTP := s.Manager.LoadSMTPConfig()
	oldChannels := s.Manager.LoadNotifyChannels()
	if user != nil {
		oldPolicy = s.Manager.LoadSecurityNotifyPolicy(user.ID)
	}
	if body.Language != nil {
		value := i18n.Normalize(*body.Language)
		if !i18n.Has(value) {
			failKey(c, http.StatusBadRequest, "api.error.language_unsupported")
			return
		}
		_ = s.Store.SetSetting("language", string(value))
	}
	set := func(key string, value *string) {
		if value != nil {
			_ = s.Store.SetSetting(key, strings.TrimSpace(*value))
		}
	}
	if body.ACMEEmail != nil {
		email := strings.ToLower(strings.TrimSpace(*body.ACMEEmail))
		if email != "" && !validEmail(email) {
			failKey(c, http.StatusBadRequest, "api.error.acme_email_invalid")
			return
		}
		_ = s.Store.SetSetting("acme.email", email)
	}
	if body.ACMECA != nil {
		if _, err := s.Manager.ResolveCA(*body.ACMECA); err != nil {
			failKey(c, http.StatusBadRequest, "task.error.ca_unsupported")
			return
		}
		_ = s.Store.SetSetting("acme.ca", *body.ACMECA)
	}
	if body.ACMECAPoolID != nil {
		if *body.ACMECAPoolID > 0 {
			pool, err := s.Store.GetCAPool(*body.ACMECAPoolID)
			if err != nil {
				failKey(c, http.StatusBadRequest, "api.error.ca_pool_not_found")
				return
			}
			if !pool.Enabled {
				failKey(c, http.StatusBadRequest, "api.error.ca_pool_disabled")
				return
			}
			if len(pool.EnabledItems()) == 0 {
				failKey(c, http.StatusBadRequest, "api.error.ca_pool_empty")
				return
			}
		}
		_ = s.Store.SetSetting(service.CAPoolSettingKey, strconv.FormatInt(*body.ACMECAPoolID, 10))
	}
	if body.ACMEKeyType != nil {
		allowed := map[string]bool{"ecdsa": true, "ecdsa384": true, "rsa2048": true, "rsa4096": true}
		if !allowed[*body.ACMEKeyType] {
			failKey(c, http.StatusBadRequest, "task.error.key_type_unsupported")
			return
		}
		_ = s.Store.SetSetting("acme.key_type", *body.ACMEKeyType)
	}
	if body.ACMERenew != nil {
		if *body.ACMERenew < 1 || *body.ACMERenew > 60 {
			failKey(c, http.StatusBadRequest, "task.error.renew_days_range")
			return
		}
		_ = s.Store.SetSetting("acme.renew_days", strconv.Itoa(*body.ACMERenew))
	}
	if body.CSRAllowedDomains != nil {
		_ = s.Store.SetSetting("csr.allowed_domains", strings.TrimSpace(*body.CSRAllowedDomains))
	}
	if body.CSRAllowAllDomains != nil {
		_ = s.Store.SetSetting("csr.allow_all_domains", strconv.FormatBool(*body.CSRAllowAllDomains))
	}
	set("smtp.host", body.SMTPHost)
	set("smtp.username", body.SMTPUsername)
	set("smtp.from", body.SMTPFrom)
	set("notify.to", body.NotifyTo)
	if body.SMTPPort != nil {
		if *body.SMTPPort < 1 || *body.SMTPPort > 65535 {
			failKey(c, http.StatusBadRequest, "api.error.smtp_port_invalid")
			return
		}
		_ = s.Store.SetSetting("smtp.port", strconv.Itoa(*body.SMTPPort))
	}
	if body.SMTPPassword != nil && strings.TrimSpace(*body.SMTPPassword) != "" {
		_ = s.Store.SetSecret("smtp.password", strings.TrimSpace(*body.SMTPPassword))
	}
	if body.SMTPSSL != nil {
		_ = s.Store.SetSetting("smtp.ssl", strconv.FormatBool(*body.SMTPSSL))
	}
	if body.NotifyEnable != nil {
		_ = s.Store.SetSetting("notify.enabled", strconv.FormatBool(*body.NotifyEnable))
	}
	if body.NotifyChannels != nil {
		channels := s.Manager.LoadNotifyChannels()
		for name, incoming := range body.NotifyChannels {
			channels[name] = service.MergeNotifyChannelConfig(name, channels[name], incoming)
		}
		if err := s.Manager.SaveNotifyChannels(channels); err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
	}
	if body.SecurityNotify != nil {
		if err := s.Manager.SaveSecurityNotifyPolicy(*body.SecurityNotify); err != nil {
			failErr(c, http.StatusInternalServerError, err)
			return
		}
	}
	s.Store.AddLogKey("settings", 0, "info", "log.settings.updated", nil)
	if user != nil {
		changes := make([]string, 0, 3)
		if body.SecurityNotify != nil && !oldPolicy.Equal(s.Manager.LoadSecurityNotifyPolicy(user.ID)) {
			changes = append(changes, "security_notify")
		}
		if oldSMTP != s.Manager.LoadSMTPConfig() {
			changes = append(changes, "smtp")
		}
		if body.NotifyChannels != nil && !reflect.DeepEqual(oldChannels, s.Manager.LoadNotifyChannels()) {
			changes = append(changes, "notify_channels")
		}
		if len(changes) > 0 && oldPolicy.Enabled {
			event := s.securityEvent(c, user.ID, model.SecurityEventNotifyChanged, map[string]string{
				"changes": strings.Join(changes, ", "),
			})
			event.AccountEmail = user.Email
			event.Recipients = service.SecurityPolicyRecipients(oldPolicy, user.Email)
			// 使用旧策略的渠道和旧 SMTP 配置快照，避免攻击者先把告警切到自己的渠道。
			event.Channels = oldPolicy.Channels
			event.SMTP = service.SnapshotSMTPConfig(oldSMTP)
			event.Force = true
			event.BypassRateLimit = true
			s.notifySecurity(event)
		}
	}
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.settings_saved", nil)})
}

func (s *Server) testMail(c *gin.Context) {
	var body struct {
		To       string `json:"to"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	_ = c.ShouldBindJSON(&body)
	// 测试邮件会用已存储的 SMTP 口令连接当前配置的主机，必须再认证，
	// 防止持有设置权限的调用方把凭据转发到自己的服务器。
	if !s.requireReauth(c, getUser(c), body.Password, body.TOTP) {
		return
	}
	if allowed, wait := s.allow(ratelimit.NotifyTest, "notifytest:"+s.clientRateKey(c)); !allowed {
		rateLimited(c, wait)
		return
	}
	to := strings.TrimSpace(body.To)
	if to == "" {
		to = s.setting("notify.to", "")
	}
	if to == "" {
		if user, err := s.Store.GetUser(); err == nil {
			to = user.Email
		}
	}
	if to == "" {
		failKey(c, http.StatusBadRequest, "api.error.recipient_required")
		return
	}
	cfg := s.Manager.LoadSMTPConfig()
	if cfg.Host == "" {
		failKey(c, http.StatusBadRequest, "api.error.smtp_config_required")
		return
	}
	current := s.managerLocale()
	html := i18n.T(current, "email.test.body", nil)
	if err := service.SendMail(cfg, to, i18n.T(current, "email.test.subject", nil), html); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	s.Store.AddLogKey("settings", 0, "info", "log.settings.test_mail_sent", map[string]any{"to": to})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.test_mail_sent", nil)})
}

func (s *Server) testChannel(c *gin.Context) {
	var body struct {
		Channel  string                      `json:"channel"`
		Config   service.NotifyChannelConfig `json:"config"`
		Password string                      `json:"password"`
		TOTP     string                      `json:"totp"`
	}
	_ = c.ShouldBindJSON(&body)
	// 测试通知会把已存储的渠道密钥按当前 URL 发出，必须再认证。
	if !s.requireReauth(c, getUser(c), body.Password, body.TOTP) {
		return
	}
	if allowed, wait := s.allow(ratelimit.NotifyTest, "notifytest:"+s.clientRateKey(c)); !allowed {
		rateLimited(c, wait)
		return
	}
	channel := strings.TrimSpace(body.Channel)
	if channel == "" {
		failKey(c, http.StatusBadRequest, "api.error.channel_required")
		return
	}
	channels := s.Manager.LoadNotifyChannels()
	cfg := service.MergeNotifyChannelConfig(channel, channels[channel], body.Config)
	current := s.managerLocale()
	if err := service.SendNotifyChannel(
		channel,
		cfg,
		i18n.T(current, "notify.test.title", nil),
		i18n.T(current, "notify.test.body", nil),
	); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	s.Store.AddLogKey("settings", 0, "info", "log.settings.test_channel_sent", map[string]any{"channel": channel})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.test_channel_sent", nil)})
}

func (s *Server) testSecurityNotify(c *gin.Context) {
	var body struct {
		Channels []string `json:"channels"`
		To       string   `json:"to"`
		Password string   `json:"password"`
		TOTP     string   `json:"totp"`
	}
	_ = c.ShouldBindJSON(&body)
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	// 测试提醒会使用当前 SMTP/渠道配置向外部发送消息，必须再认证。
	if !s.requireReauth(c, user, body.Password, body.TOTP) {
		return
	}
	if allowed, wait := s.allow(ratelimit.NotifyTest, "securitytest:"+s.clientRateKey(c)); !allowed {
		rateLimited(c, wait)
		return
	}
	if err := s.Manager.SendSecurityTest(user.ID, body.Channels, body.To); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	s.Store.AddLogKey("settings", user.ID, "info", "log.settings.test_security_notify_sent", nil)
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.security_test_queued", nil)})
}

func (s *Server) changeEntry(c *gin.Context) {
	if allowed, wait := s.allow(ratelimit.EntryChange, "entry-change:"+s.clientRateKey(c)); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		Entry    string `json:"entry"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.requireReauth(c, getUser(c), body.Password, body.TOTP) {
		return
	}
	entry, err := util.ValidateEntry(body.Entry)
	if err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	if err := s.Store.SetSetting("security.entry", entry); err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.save_entry_failed")
		return
	}
	s.SetEntry(entry)
	s.Store.AddLogKey("settings", 0, "warning", "log.settings.entry_changed", map[string]any{"entry": util.MaskSecret(entry)})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.entry_updated", nil)})
}
