package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
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
	ok(c, gin.H{
		"version":       Version,
		"language":      string(current),
		"languages":     supportedLanguages(),
		"cas":           service.LocalizedCADefs(s.Store, current),
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
	ok(c, data)
}

func (s *Server) getSettings(c *gin.Context) {
	smtpPassword, _ := s.Store.GetSecret("smtp.password")
	gotifyToken, _ := s.Store.GetSecret("gotify.token")
	gotifyPriority, _ := strconv.Atoi(s.setting("gotify.priority", "5"))
	serverChanSendKey, _ := s.Store.GetSecret("serverchan.sendkey")
	stepUp := make(map[string]bool, len(toggleableStepUpActions))
	for _, action := range toggleableStepUpActions {
		stepUp[action] = s.stepUpEnabled(action)
	}
	ok(c, gin.H{
		"step_up":                stepUp,
		"entry":                  s.Entry(),
		"verify_mode":            s.effectiveVerifyMode(getUser(c)),
		"language":               s.setting("language", string(i18n.Default)),
		"acme_email":             s.setting("acme.email", ""),
		"acme_ca":                s.setting("acme.ca", "letsencrypt"),
		"acme_key_type":          s.setting("acme.key_type", "ecdsa"),
		"acme_renew_days":        s.settingInt("acme.renew_days", 30),
		"smtp_host":              s.setting("smtp.host", ""),
		"smtp_port":              s.settingInt("smtp.port", 465),
		"smtp_username":          s.setting("smtp.username", ""),
		"smtp_from":              s.setting("smtp.from", ""),
		"smtp_ssl":               s.setting("smtp.ssl", "true") == "true",
		"smtp_password_set":      smtpPassword != "",
		"notify_enabled":         s.setting("notify.enabled", "false") == "true",
		"notify_to":              s.setting("notify.to", ""),
		"gotify_url":             s.setting("gotify.url", ""),
		"gotify_token_set":       gotifyToken != "",
		"gotify_enabled":         s.setting("gotify.enabled", "false") == "true",
		"gotify_priority":        gotifyPriority,
		"serverchan_enabled":     s.setting("serverchan.enabled", "false") == "true",
		"serverchan_sendkey_set": serverChanSendKey != "",
		"serverchan_version":     s.setting("serverchan.version", "auto"),
		"notify_channels":        s.Manager.NotifyChannelsForUI(),
	})
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
		Language          *string                                `json:"language"`
		ACMEEmail         *string                                `json:"acme_email"`
		ACMECA            *string                                `json:"acme_ca"`
		ACMEKeyType       *string                                `json:"acme_key_type"`
		ACMERenew         *int                                   `json:"acme_renew_days"`
		SMTPHost          *string                                `json:"smtp_host"`
		SMTPPort          *int                                   `json:"smtp_port"`
		SMTPUsername      *string                                `json:"smtp_username"`
		SMTPPassword      *string                                `json:"smtp_password"`
		SMTPFrom          *string                                `json:"smtp_from"`
		SMTPSSL           *bool                                  `json:"smtp_ssl"`
		NotifyEnable      *bool                                  `json:"notify_enabled"`
		NotifyTo          *string                                `json:"notify_to"`
		GotifyURL         *string                                `json:"gotify_url"`
		GotifyToken       *string                                `json:"gotify_token"`
		GotifyEnable      *bool                                  `json:"gotify_enabled"`
		GotifyPrio        *int                                   `json:"gotify_priority"`
		ServerChanKey     *string                                `json:"serverchan_sendkey"`
		ServerChanEnable  *bool                                  `json:"serverchan_enabled"`
		ServerChanVersion *string                                `json:"serverchan_version"`
		NotifyChannels    map[string]service.NotifyChannelConfig `json:"notify_channels"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	// 语言切换等纯展示设置不要求二次验证；SMTP/通知/默认 CA 等会改变
	// 凭据流向或签发目标的配置必须完成二次验证。
	sensitive := body.ACMEEmail != nil || body.ACMECA != nil || body.ACMEKeyType != nil ||
		body.ACMERenew != nil || body.SMTPHost != nil || body.SMTPPort != nil ||
		body.SMTPUsername != nil || body.SMTPPassword != nil || body.SMTPFrom != nil ||
		body.SMTPSSL != nil || body.NotifyEnable != nil || body.NotifyTo != nil ||
		body.GotifyURL != nil || body.GotifyToken != nil || body.GotifyEnable != nil ||
		body.GotifyPrio != nil || body.ServerChanKey != nil || body.ServerChanEnable != nil ||
		body.ServerChanVersion != nil || body.NotifyChannels != nil
	if sensitive && !s.requireSecondaryVerify(c, secondaryActionSettingsWrite) {
		return
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
	if body.GotifyURL != nil {
		value := strings.TrimSpace(*body.GotifyURL)
		if value != "" {
			if err := service.ValidateOutboundURL(value); err != nil {
				failKey(c, http.StatusBadRequest, "api.error.gotify_url_invalid")
				return
			}
		}
		_ = s.Store.SetSetting("gotify.url", value)
	}
	if body.GotifyToken != nil && strings.TrimSpace(*body.GotifyToken) != "" {
		_ = s.Store.SetSecret("gotify.token", strings.TrimSpace(*body.GotifyToken))
	}
	if body.GotifyEnable != nil {
		_ = s.Store.SetSetting("gotify.enabled", strconv.FormatBool(*body.GotifyEnable))
	}
	if body.GotifyPrio != nil {
		if *body.GotifyPrio < 0 || *body.GotifyPrio > 10 {
			failKey(c, http.StatusBadRequest, "api.error.gotify_priority_invalid")
			return
		}
		_ = s.Store.SetSetting("gotify.priority", strconv.Itoa(*body.GotifyPrio))
	}
	if body.ServerChanKey != nil && strings.TrimSpace(*body.ServerChanKey) != "" {
		_ = s.Store.SetSecret("serverchan.sendkey", strings.TrimSpace(*body.ServerChanKey))
	}
	if body.ServerChanEnable != nil {
		_ = s.Store.SetSetting("serverchan.enabled", strconv.FormatBool(*body.ServerChanEnable))
	}
	if body.ServerChanVersion != nil {
		version := strings.TrimSpace(*body.ServerChanVersion)
		allowed := map[string]bool{"auto": true, "turbo": true, "sc3": true}
		if !allowed[version] {
			failKey(c, http.StatusBadRequest, "api.error.serverchan_version_invalid")
			return
		}
		_ = s.Store.SetSetting("serverchan.version", version)
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
	s.Store.AddLogKey("settings", 0, "info", "log.settings.updated", nil)
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.settings_saved", nil)})
}

func (s *Server) testMail(c *gin.Context) {
	if !s.requireSecondaryVerify(c, secondaryActionNotificationTest) {
		return
	}
	if allowed, wait := s.limiter.Allow("notifytest:"+clientRateKey(c), 20, time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		To string `json:"to"`
	}
	_ = c.ShouldBindJSON(&body)
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

func (s *Server) testGotify(c *gin.Context) {
	if !s.requireSecondaryVerify(c, secondaryActionNotificationTest) {
		return
	}
	if allowed, wait := s.limiter.Allow("notifytest:"+clientRateKey(c), 20, time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		URL      string `json:"url"`
		Token    string `json:"token"`
		Priority *int   `json:"priority"`
	}
	_ = c.ShouldBindJSON(&body)
	cfg := s.Manager.LoadGotifyConfig()
	if value := strings.TrimSpace(body.URL); value != "" {
		cfg.URL = value
	}
	if value := strings.TrimSpace(body.Token); value != "" {
		cfg.Token = value
	}
	if body.Priority != nil && *body.Priority >= 0 && *body.Priority <= 10 {
		cfg.Priority = *body.Priority
	}
	if cfg.URL == "" || cfg.Token == "" {
		failKey(c, http.StatusBadRequest, "api.error.gotify_config_required")
		return
	}
	current := s.managerLocale()
	if err := service.SendGotify(
		cfg,
		i18n.T(current, "gotify.test.title", nil),
		i18n.T(current, "gotify.test.body", nil),
	); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	s.Store.AddLogKey("settings", 0, "info", "log.settings.test_gotify_sent", nil)
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.test_gotify_sent", nil)})
}

func (s *Server) testServerChan(c *gin.Context) {
	if !s.requireSecondaryVerify(c, secondaryActionNotificationTest) {
		return
	}
	if allowed, wait := s.limiter.Allow("notifytest:"+clientRateKey(c), 20, time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		SendKey string `json:"sendkey"`
		Version string `json:"version"`
	}
	_ = c.ShouldBindJSON(&body)
	cfg := s.Manager.LoadServerChanConfig()
	if value := strings.TrimSpace(body.SendKey); value != "" {
		cfg.SendKey = value
	}
	if value := strings.TrimSpace(body.Version); value != "" {
		cfg.Version = value
	}
	if cfg.SendKey == "" {
		failKey(c, http.StatusBadRequest, "api.error.serverchan_config_required")
		return
	}
	current := s.managerLocale()
	if err := service.SendServerChan(
		cfg,
		i18n.T(current, "serverchan.test.title", nil),
		i18n.T(current, "serverchan.test.body", nil),
	); err != nil {
		failErr(c, http.StatusBadRequest, err)
		return
	}
	s.Store.AddLogKey("settings", 0, "info", "log.settings.test_serverchan_sent", nil)
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.test_serverchan_sent", nil)})
}

func (s *Server) testChannel(c *gin.Context) {
	if !s.requireSecondaryVerify(c, secondaryActionNotificationTest) {
		return
	}
	if allowed, wait := s.limiter.Allow("notifytest:"+clientRateKey(c), 20, time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		Channel string                      `json:"channel"`
		Config  service.NotifyChannelConfig `json:"config"`
	}
	_ = c.ShouldBindJSON(&body)
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

func (s *Server) changeEntry(c *gin.Context) {
	if !s.requireSecondaryVerify(c, secondaryActionEntryChange) {
		return
	}
	var body struct {
		Entry string `json:"entry"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
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
	ok(c, gin.H{"entry": entry, "message": i18n.T(locale(c), "api.ok.entry_updated", nil)})
}
