package service

import (
	"encoding/json"
	"net"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

const (
	securityNotifyEnabledKey         = "security.notify.enabled"
	securityNotifyEventsKey          = "security.notify.events"
	securityNotifyToKey              = "security.notify.to"
	securityNotifyRetentionKey       = "security.notify.retention_days"
	securityNotifyLoginKey           = "security.notify.login"
	securityNotifyFailedLoginKey     = "security.notify.failed_login"
	securityNotifyFailedThresholdKey = "security.notify.failed_threshold"
	securityNotifyFailedWindowKey    = "security.notify.failed_window"
	securityNotifyNewDeviceDaysKey   = "security.notify.new_device_days"
	securityNotifyChannelsKey        = "security.notify.channels"
	securityNotifyMaxPerHourKey      = "security.notify.max_per_hour"

	securityNotifyDefaultRetentionDays   = 90
	securityNotifyMinRetentionDays       = 7
	securityNotifyMaxRetentionDays       = 365
	securityNotifyDefaultNewDeviceDays   = 30
	securityNotifyMinNewDeviceDays       = 1
	securityNotifyMaxNewDeviceDays       = 365
	securityNotifyDefaultFailedThreshold = 5
	securityNotifyMinFailedThreshold     = 1
	securityNotifyMaxFailedThreshold     = 100
	securityNotifyDefaultFailedWindow    = 600
	securityNotifyMinFailedWindow        = 60
	securityNotifyMaxFailedWindow        = 86400
	securityNotifyDefaultMaxPerHour      = 10
	securityNotifyMinMaxPerHour          = 1
	securityNotifyMaxMaxPerHour          = 1000
	securityNotifyFailureAlertInterval   = time.Hour
	securityNotifyMaxRecipients          = 10
	securityNotifyMaxDetailLength        = 256
	securityNotifyMaxUserAgentLength     = 256
	securityNotifyMaxAttempts            = 3
	securityNotifyRetryBase              = time.Minute
	securityNotifyRetryStep              = 4 * time.Minute
)

// SecurityNotifyPolicy 是安全提醒策略。策略存 settings 表，与证书通知开关
// 完全独立，避免关闭证书邮件后安全提醒一起失明。
type SecurityNotifyPolicy struct {
	Enabled         bool       `json:"enabled"`
	Events          []string   `json:"events"`
	To              string     `json:"to"`
	RetentionDays   int        `json:"retention_days"`
	Login           string     `json:"login"`
	FailedLogin     bool       `json:"failed_login"`
	FailedThreshold int        `json:"failed_threshold"`
	FailedWindow    int        `json:"failed_window"`
	NewDeviceDays   int        `json:"new_device_days"`
	Channels        []string   `json:"channels"`
	MaxPerHour      int        `json:"max_per_hour"`
	LastError       string     `json:"last_error,omitempty"`
	LastDeliveredAt *time.Time `json:"last_delivered_at,omitempty"`
}

// DefaultSecurityNotifyPolicy 返回安全默认值：核心事件全开、登录仅新设备、
// 失败登录聚合开启、默认只走邮件、保留 90 天。
func DefaultSecurityNotifyPolicy() SecurityNotifyPolicy {
	return SecurityNotifyPolicy{
		Enabled:         true,
		Events:          model.SecurityNotifyEventKinds(),
		RetentionDays:   securityNotifyDefaultRetentionDays,
		Login:           "new",
		FailedLogin:     true,
		FailedThreshold: securityNotifyDefaultFailedThreshold,
		FailedWindow:    securityNotifyDefaultFailedWindow,
		NewDeviceDays:   securityNotifyDefaultNewDeviceDays,
		Channels:        []string{"mail"},
		MaxPerHour:      securityNotifyDefaultMaxPerHour,
	}
}

// LoadSecurityNotifyPolicy 读取策略，并附带最近一次投递状态。
// userID 为 0 时只读策略，不查状态（供后台清理等场景使用）。
func (m *Manager) LoadSecurityNotifyPolicy(userID int64) SecurityNotifyPolicy {
	policy := DefaultSecurityNotifyPolicy()
	if raw, _ := m.Store.GetSetting(securityNotifyEnabledKey); strings.TrimSpace(raw) != "" {
		policy.Enabled = strings.EqualFold(strings.TrimSpace(raw), "true")
	}
	if raw, _ := m.Store.GetSetting(securityNotifyEventsKey); strings.TrimSpace(raw) != "" {
		var events []string
		if json.Unmarshal([]byte(raw), &events) == nil {
			policy.Events = normalizeSecurityEvents(events)
		}
	}
	if raw, _ := m.Store.GetSetting(securityNotifyToKey); strings.TrimSpace(raw) != "" {
		policy.To = strings.TrimSpace(raw)
	}
	if raw, _ := m.Store.GetSetting(securityNotifyRetentionKey); strings.TrimSpace(raw) != "" {
		if days, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			policy.RetentionDays = days
		}
	}
	if raw, _ := m.Store.GetSetting(securityNotifyLoginKey); strings.TrimSpace(raw) != "" {
		policy.Login = normalizeSecurityLoginMode(raw)
	}
	if raw, _ := m.Store.GetSetting(securityNotifyFailedLoginKey); strings.TrimSpace(raw) != "" {
		policy.FailedLogin = strings.EqualFold(strings.TrimSpace(raw), "true")
	}
	if raw, _ := m.Store.GetSetting(securityNotifyFailedThresholdKey); strings.TrimSpace(raw) != "" {
		if value, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			policy.FailedThreshold = value
		}
	}
	if raw, _ := m.Store.GetSetting(securityNotifyFailedWindowKey); strings.TrimSpace(raw) != "" {
		if value, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			policy.FailedWindow = value
		}
	}
	if raw, _ := m.Store.GetSetting(securityNotifyNewDeviceDaysKey); strings.TrimSpace(raw) != "" {
		if value, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			policy.NewDeviceDays = value
		}
	}
	if raw, _ := m.Store.GetSetting(securityNotifyChannelsKey); strings.TrimSpace(raw) != "" {
		var channels []string
		if json.Unmarshal([]byte(raw), &channels) == nil {
			policy.Channels = normalizeSecurityChannels(channels)
		}
	}
	if raw, _ := m.Store.GetSetting(securityNotifyMaxPerHourKey); strings.TrimSpace(raw) != "" {
		if value, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			policy.MaxPerHour = value
		}
	}
	policy.RetentionDays = clampSecurityRetention(policy.RetentionDays)
	policy.MaxPerHour = clampSecurityMaxPerHour(policy.MaxPerHour)
	policy.FailedThreshold = clampSecurityFailedThreshold(policy.FailedThreshold)
	policy.FailedWindow = clampSecurityFailedWindow(policy.FailedWindow)
	policy.NewDeviceDays = clampSecurityNewDeviceDays(policy.NewDeviceDays)
	policy.Channels = normalizeSecurityChannels(policy.Channels)
	if userID > 0 {
		if lastError, deliveredAt, err := m.Store.LatestSecurityEventStatus(userID); err == nil {
			policy.LastError = lastError
			policy.LastDeliveredAt = deliveredAt
		}
	}
	return policy
}

// ValidateSecurityNotifyPolicy 校验策略，不写库。API 层应在写入其他设置前
// 先调用，避免策略非法时出现“SMTP 已改、安全策略没保存”的半更新。
func ValidateSecurityNotifyPolicy(policy SecurityNotifyPolicy) error {
	for _, kind := range policy.Events {
		if value := strings.TrimSpace(kind); value != "" && !model.IsSecurityNotifyEventKind(value) {
			return i18n.E("api.error.security_notify_event_invalid", "event", value)
		}
	}
	if value := strings.TrimSpace(policy.Login); value != "" && !isSecurityLoginMode(value) {
		return i18n.E("api.error.security_notify_login_invalid")
	}
	if policy.FailedThreshold != 0 &&
		(policy.FailedThreshold < securityNotifyMinFailedThreshold || policy.FailedThreshold > securityNotifyMaxFailedThreshold) {
		return i18n.E("api.error.security_notify_threshold_invalid",
			"min", securityNotifyMinFailedThreshold, "max", securityNotifyMaxFailedThreshold)
	}
	if policy.FailedWindow != 0 &&
		(policy.FailedWindow < securityNotifyMinFailedWindow || policy.FailedWindow > securityNotifyMaxFailedWindow) {
		return i18n.E("api.error.security_notify_window_invalid",
			"min", securityNotifyMinFailedWindow, "max", securityNotifyMaxFailedWindow)
	}
	if policy.NewDeviceDays != 0 &&
		(policy.NewDeviceDays < securityNotifyMinNewDeviceDays || policy.NewDeviceDays > securityNotifyMaxNewDeviceDays) {
		return i18n.E("api.error.security_notify_new_device_invalid",
			"min", securityNotifyMinNewDeviceDays, "max", securityNotifyMaxNewDeviceDays)
	}
	if policy.MaxPerHour != 0 &&
		(policy.MaxPerHour < securityNotifyMinMaxPerHour || policy.MaxPerHour > securityNotifyMaxMaxPerHour) {
		return i18n.E("api.error.security_notify_max_per_hour_invalid",
			"min", securityNotifyMinMaxPerHour, "max", securityNotifyMaxMaxPerHour)
	}
	for _, channel := range policy.Channels {
		value := strings.ToLower(strings.TrimSpace(channel))
		if value == "" {
			continue
		}
		if !IsNotifyChannel(value) {
			return i18n.E("api.error.security_notify_channel_invalid", "channel", value)
		}
	}
	_, err := canonicalSecurityRecipients(policy.To)
	return err
}

// SaveSecurityNotifyPolicy 校验并保存策略。
func (m *Manager) SaveSecurityNotifyPolicy(policy SecurityNotifyPolicy) error {
	if err := ValidateSecurityNotifyPolicy(policy); err != nil {
		return err
	}
	events := normalizeSecurityEvents(policy.Events)
	if policy.Events == nil {
		// 缺省字段按默认全开发送；显式空数组才表示全部关闭。
		events = model.SecurityNotifyEventKinds()
	}
	to, err := canonicalSecurityRecipients(policy.To)
	if err != nil {
		return err
	}
	retention := clampSecurityRetention(policy.RetentionDays)
	login := normalizeSecurityLoginMode(policy.Login)
	threshold := clampSecurityFailedThreshold(policy.FailedThreshold)
	window := clampSecurityFailedWindow(policy.FailedWindow)
	newDeviceDays := clampSecurityNewDeviceDays(policy.NewDeviceDays)
	channels := normalizeSecurityChannels(policy.Channels)
	maxPerHour := clampSecurityMaxPerHour(policy.MaxPerHour)
	encoded, err := json.Marshal(events)
	if err != nil {
		return err
	}
	encodedChannels, err := json.Marshal(channels)
	if err != nil {
		return err
	}
	if err := m.Store.SetSetting(securityNotifyEnabledKey, strconv.FormatBool(policy.Enabled)); err != nil {
		return err
	}
	if err := m.Store.SetSetting(securityNotifyEventsKey, string(encoded)); err != nil {
		return err
	}
	if err := m.Store.SetSetting(securityNotifyToKey, to); err != nil {
		return err
	}
	if err := m.Store.SetSetting(securityNotifyRetentionKey, strconv.Itoa(retention)); err != nil {
		return err
	}
	if err := m.Store.SetSetting(securityNotifyLoginKey, login); err != nil {
		return err
	}
	if err := m.Store.SetSetting(securityNotifyFailedLoginKey, strconv.FormatBool(policy.FailedLogin)); err != nil {
		return err
	}
	if err := m.Store.SetSetting(securityNotifyFailedThresholdKey, strconv.Itoa(threshold)); err != nil {
		return err
	}
	if err := m.Store.SetSetting(securityNotifyFailedWindowKey, strconv.Itoa(window)); err != nil {
		return err
	}
	if err := m.Store.SetSetting(securityNotifyNewDeviceDaysKey, strconv.Itoa(newDeviceDays)); err != nil {
		return err
	}
	if err := m.Store.SetSetting(securityNotifyChannelsKey, string(encodedChannels)); err != nil {
		return err
	}
	return m.Store.SetSetting(securityNotifyMaxPerHourKey, strconv.Itoa(maxPerHour))
}

// Equal 比较两个策略的业务字段，忽略最近投递状态。
func (p SecurityNotifyPolicy) Equal(other SecurityNotifyPolicy) bool {
	if p.Enabled != other.Enabled ||
		clampSecurityRetention(p.RetentionDays) != clampSecurityRetention(other.RetentionDays) ||
		normalizeSecurityLoginMode(p.Login) != normalizeSecurityLoginMode(other.Login) ||
		p.FailedLogin != other.FailedLogin ||
		clampSecurityFailedThreshold(p.FailedThreshold) != clampSecurityFailedThreshold(other.FailedThreshold) ||
		clampSecurityFailedWindow(p.FailedWindow) != clampSecurityFailedWindow(other.FailedWindow) ||
		clampSecurityNewDeviceDays(p.NewDeviceDays) != clampSecurityNewDeviceDays(other.NewDeviceDays) ||
		clampSecurityMaxPerHour(p.MaxPerHour) != clampSecurityMaxPerHour(other.MaxPerHour) {
		return false
	}
	if securityRecipientKey(p.To) != securityRecipientKey(other.To) {
		return false
	}
	if !equalStringSet(normalizeSecurityEvents(p.Events), normalizeSecurityEvents(other.Events)) {
		return false
	}
	return equalStringSet(normalizeSecurityChannels(p.Channels), normalizeSecurityChannels(other.Channels))
}

// LoginNotifyEnabled 根据登录策略判断当前登录是否需要提醒。
func (p SecurityNotifyPolicy) LoginNotifyEnabled(newDevice bool) bool {
	switch normalizeSecurityLoginMode(p.Login) {
	case "always":
		return true
	case "new":
		return newDevice
	default:
		return false
	}
}

// EventEnabled 判断事件类型是否在当前策略中启用。
func (p SecurityNotifyPolicy) EventEnabled(kind string) bool {
	for _, item := range normalizeSecurityEvents(p.Events) {
		if item == kind {
			return true
		}
	}
	return false
}

// SecurityPolicyRecipients 返回策略对应的邮件收件人：账户邮箱 + 额外收件人。
// 不包含 notify.to，因为 notify.to 可能是一个团队群组地址。
func SecurityPolicyRecipients(policy SecurityNotifyPolicy, accountEmail string) []string {
	recipients := make([]string, 0, 1+securityNotifyMaxRecipients)
	if value := strings.ToLower(strings.TrimSpace(accountEmail)); value != "" {
		recipients = append(recipients, value)
	}
	extra, _ := parseSecurityRecipients(policy.To)
	recipients = append(recipients, extra...)
	return dedupeSecurityRecipients(recipients)
}

// NotifySecurity 写入一条需要投递的安全事件并唤醒后台 worker。
// 事件落库失败时返回错误，调用方只记录日志，不回滚业务操作。
func (m *Manager) NotifySecurity(event model.SecurityEvent) error {
	if strings.TrimSpace(event.Kind) == "" {
		return i18n.E("api.error.invalid_params")
	}
	policy := m.LoadSecurityNotifyPolicy(0)
	if !event.Force && (!policy.Enabled || !policy.EventEnabled(event.Kind)) {
		return nil
	}
	// 串行化“计数 + 摘要判断 + 插入”，避免并发事件在同一小时写出多条摘要。
	m.securityRateMu.Lock()
	now := time.Now().UTC()
	if !event.BypassRateLimit && policy.MaxPerHour > 0 && event.UserID > 0 {
		since := now.Add(-time.Hour)
		if count, err := m.Store.CountSecurityEventsSince(event.UserID, since); err == nil && count >= policy.MaxPerHour {
			// 超出上限的事件仍然落库为“仅记录”，保留审计和最近事件面板，
			// 但不进入投递队列。
			_ = m.RecordSecurityEvent(event)
			exists, _ := m.Store.HasSecurityEventKindSince(event.UserID, model.SecurityEventDigest, since)
			if exists {
				m.securityRateMu.Unlock()
				return nil
			}
			// 超出上限后只发送一条摘要，避免大量事件刷爆邮箱/渠道。
			event.Kind = model.SecurityEventDigest
			event.Detail = map[string]string{
				"count": strconv.Itoa(count + 1),
				"limit": strconv.Itoa(policy.MaxPerHour),
			}
			event.Recipients = nil
			event.Channels = nil
			event.SMTP = nil
			event.Deliveries = nil
			event.LastError = ""
			event.Force = true
			event.BypassRateLimit = true
		}
	}
	event = m.prepareSecurityEvent(event, policy)
	event.Notify = true
	err := m.Store.CreateSecurityEvent(&event)
	m.securityRateMu.Unlock()
	if err != nil {
		return err
	}
	m.wakeSecurityWorker()
	return nil
}

// RecordSecurityEvent 只记录事件，不进入投递队列。用于登录历史等
// “即使关闭通知也要保留设备指纹”的场景。
func (m *Manager) RecordSecurityEvent(event model.SecurityEvent) error {
	if strings.TrimSpace(event.Kind) == "" {
		return i18n.E("api.error.invalid_params")
	}
	policy := m.LoadSecurityNotifyPolicy(0)
	event = m.prepareSecurityEvent(event, policy)
	event.Notify = false
	return m.Store.CreateSecurityEvent(&event)
}

// SendSecurityTest 使用当前表单的渠道和收件人写入一条测试安全提醒。
// 测试事件通过 worker 异步投递，设置页可查看最近投递状态。
func (m *Manager) SendSecurityTest(userID int64, channels []string, to string) error {
	user, err := m.Store.GetUserByID(userID)
	if err != nil {
		return err
	}
	if user == nil {
		return i18n.E("api.error.unauthorized")
	}
	extra, err := parseSecurityRecipients(to)
	if err != nil {
		return err
	}
	normalized, err := validateSecurityChannels(channels)
	if err != nil {
		return err
	}
	if !m.securityChannelDeliverable(normalized) {
		return i18n.E("api.error.security_notify_no_delivery")
	}
	recipients := dedupeSecurityRecipients(append([]string{user.Email}, extra...))
	event := model.SecurityEvent{
		UserID:          userID,
		Kind:            model.SecurityEventTest,
		AccountEmail:    user.Email,
		Recipients:      recipients,
		Channels:        normalized,
		Force:           true,
		BypassRateLimit: true,
		Detail:          map[string]string{"test": "true"},
	}
	return m.NotifySecurity(event)
}

// prepareSecurityEvent 统一补齐收件人、渠道、时间和转义，避免各调用点重复。
func (m *Manager) prepareSecurityEvent(event model.SecurityEvent, policy SecurityNotifyPolicy) model.SecurityEvent {
	if event.AccountEmail == "" {
		if user, err := m.Store.GetUserByID(event.UserID); err == nil && user != nil {
			event.AccountEmail = user.Email
		}
	}
	recipients := dedupeSecurityRecipients(event.Recipients)
	if len(recipients) == 0 {
		recipients = SecurityPolicyRecipients(policy, event.AccountEmail)
	}
	event.Recipients = recipients
	if len(event.Channels) == 0 {
		event.Channels = policy.Channels
	}
	event.Channels = normalizeSecurityChannels(event.Channels)
	event.UserAgent = sanitizeSecurityText(event.UserAgent, securityNotifyMaxUserAgentLength)
	event.Fingerprint = sanitizeSecurityText(event.Fingerprint, 128)
	if event.IP != "" && net.ParseIP(event.IP) == nil {
		event.IP = ""
	}
	if event.Detail == nil {
		event.Detail = map[string]string{}
	}
	for key, value := range event.Detail {
		event.Detail[key] = sanitizeSecurityText(value, securityNotifyMaxDetailLength)
	}
	if event.Deliveries == nil {
		event.Deliveries = map[string]string{}
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.NextAttemptAt = event.CreatedAt
	return event
}

type securityFailureState struct {
	Times     []time.Time
	LastAlert time.Time
	IPs       []string
}

// LoginFailureResult 描述一次登录失败记录后的聚合结果。
type LoginFailureResult struct {
	Count         int
	WindowSeconds int
	IPs           []string
	Notify        bool
}

// RecordLoginFailure 记录一次登录失败并判断是否达到告警阈值。
// 已登录账户按账户维度聚合；未知邮箱按来源 IP 聚合，避免通过告警差异枚举邮箱。
func (m *Manager) RecordLoginFailure(userID int64, ip string, now time.Time) LoginFailureResult {
	policy := m.LoadSecurityNotifyPolicy(0)
	result := LoginFailureResult{WindowSeconds: policy.FailedWindow}
	if !policy.Enabled || !policy.FailedLogin {
		return result
	}
	key := "ip:" + strings.TrimSpace(ip)
	if userID > 0 {
		key = "user:" + strconv.FormatInt(userID, 10)
	}
	if strings.TrimSpace(ip) == "" && userID == 0 {
		key = "ip:unknown"
	}
	window := time.Duration(policy.FailedWindow) * time.Second

	m.securityFailureMu.Lock()
	defer m.securityFailureMu.Unlock()
	if m.securityFailures == nil {
		m.securityFailures = make(map[string]*securityFailureState)
	}
	state := m.securityFailures[key]
	if state == nil {
		// 与内存限流器保持同一量级的硬上限，避免大量来源 IP 撑大内存。
		if len(m.securityFailures) >= 10000 {
			return result
		}
		state = &securityFailureState{}
		m.securityFailures[key] = state
	}
	cutoff := now.Add(-window)
	kept := state.Times[:0]
	for _, item := range state.Times {
		if item.After(cutoff) {
			kept = append(kept, item)
		}
	}
	state.Times = append(kept, now)
	if value := strings.TrimSpace(ip); value != "" {
		exists := false
		for _, item := range state.IPs {
			if item == value {
				exists = true
				break
			}
		}
		if !exists && len(state.IPs) < 5 {
			state.IPs = append(state.IPs, value)
		}
	}
	result.Count = len(state.Times)
	result.IPs = append([]string(nil), state.IPs...)
	if result.Count >= policy.FailedThreshold &&
		(state.LastAlert.IsZero() || now.Sub(state.LastAlert) >= securityNotifyFailureAlertInterval) {
		result.Notify = true
		state.LastAlert = now
		state.Times = nil
		state.IPs = nil
	}
	// 清理长期无活动的 key，避免攻击者用大量来源 IP 撑大内存。
	for name, item := range m.securityFailures {
		if len(item.Times) == 0 && now.Sub(item.LastAlert) > 2*securityNotifyFailureAlertInterval {
			delete(m.securityFailures, name)
		}
	}
	return result
}

// ResetLoginFailures 在登录成功后清空账户与当前 IP 的失败计数。
func (m *Manager) ResetLoginFailures(userID int64, ip string) {
	m.securityFailureMu.Lock()
	defer m.securityFailureMu.Unlock()
	if userID > 0 {
		delete(m.securityFailures, "user:"+strconv.FormatInt(userID, 10))
	}
	if value := strings.TrimSpace(ip); value != "" {
		delete(m.securityFailures, "ip:"+value)
	}
	delete(m.securityFailures, "ip:unknown")
}

func (m *Manager) wakeSecurityWorker() {
	m.securityOnce.Do(func() {
		m.securityWake = make(chan struct{}, 1)
		go m.securityLoop()
	})
	select {
	case m.securityWake <- struct{}{}:
	default:
	}
}

func (m *Manager) securityLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		m.processSecurityEvents()
		select {
		case <-m.securityWake:
		case <-ticker.C:
		}
	}
}

func (m *Manager) processSecurityEvents() {
	events, err := m.Store.ListPendingSecurityEvents(time.Now().UTC(), 50)
	if err != nil {
		return
	}
	for index := range events {
		m.deliverSecurityEvent(&events[index])
	}
	policy := m.LoadSecurityNotifyPolicy(0)
	_, _ = m.Store.CleanupSecurityEvents(time.Now().UTC().AddDate(0, 0, -policy.RetentionDays))
}

func (m *Manager) deliverSecurityEvent(event *model.SecurityEvent) {
	if event == nil {
		return
	}
	if len(event.Channels) == 0 {
		event.Channels = []string{"mail"}
	}
	attempts := event.Attempts + 1
	deliveries := event.Deliveries
	if deliveries == nil {
		deliveries = map[string]string{}
	}
	cfg := m.LoadSMTPConfig()
	if event.SMTP != nil {
		cfg = smtpConfigFromSnapshot(event.SMTP)
	}
	channelConfigs := m.LoadNotifyChannels()
	subject, htmlBody := m.renderSecurityEmail(*event)
	textBody := htmlToText(htmlBody)
	if link := strings.TrimSpace(event.Link); link != "" {
		textBody += "\n\n" + link
	}
	mailEnabled := false
	for _, channel := range event.Channels {
		if channel == "mail" {
			mailEnabled = true
			break
		}
	}
	lastError := ""
	required := make([]string, 0, len(event.Recipients)+len(event.Channels))
	if mailEnabled {
		for _, recipient := range event.Recipients {
			key := securityDeliveryKey(recipient)
			required = append(required, key)
			if deliveries[key] == "sent" {
				continue
			}
			if cfg.Host == "" || cfg.From == "" {
				deliveries[key] = "failed"
				lastError = "smtp not configured"
				continue
			}
			if err := m.sendSecurityMail(cfg, recipient, subject, htmlBody); err != nil {
				deliveries[key] = "failed"
				lastError = SafeError(err)
				continue
			}
			deliveries[key] = "sent"
		}
	}
	for _, channel := range event.Channels {
		if channel == "mail" {
			continue
		}
		key := securityChannelKey(channel)
		if deliveries[key] == "sent" {
			required = append(required, key)
			continue
		}
		channelCfg, found := channelConfigs[channel]
		if !found || !NotifyChannelConfigEnabled(channelCfg) {
			deliveries[key] = "skipped"
			m.Store.AddLogKey("notify", event.UserID, "warning", "log.security.notify_channel_skipped", map[string]any{
				"channel": channel,
			})
			continue
		}
		required = append(required, key)
		if err := m.sendSecurityChannel(channel, channelCfg, subject, textBody); err != nil {
			deliveries[key] = "failed"
			lastError = SafeError(err)
			continue
		}
		deliveries[key] = "sent"
	}
	if len(required) == 0 {
		m.finishSecurityEvent(event, attempts, "no recipient or channel")
		return
	}
	allSent := true
	for _, key := range required {
		if deliveries[key] != "sent" {
			allSent = false
			break
		}
	}
	now := time.Now().UTC()
	if allSent {
		if err := m.Store.UpdateSecurityEventDelivery(event.ID, deliveries, attempts, "", nil, &now); err == nil {
			m.Store.AddLogKey("notify", event.UserID, "info", "log.security.notify_delivered", map[string]any{
				"kind":  event.Kind,
				"count": len(required),
			})
		}
		return
	}
	if lastError == "" {
		lastError = "delivery failed"
	}
	if attempts >= securityNotifyMaxAttempts {
		_ = m.Store.UpdateSecurityEventDelivery(event.ID, deliveries, attempts, lastError, nil, nil)
		m.Store.AddLogKey("notify", event.UserID, "error", "log.security.notify_failed", map[string]any{
			"kind":  event.Kind,
			"error": lastError,
		})
		return
	}
	next := now.Add(securityRetryDelay(attempts))
	if err := m.Store.UpdateSecurityEventDelivery(event.ID, deliveries, attempts, lastError, &next, nil); err == nil {
		m.Store.AddLogKey("notify", event.UserID, "warning", "log.security.notify_failed", map[string]any{
			"kind":  event.Kind,
			"error": lastError,
		})
	}
}

func (m *Manager) securityChannelDeliverable(channels []string) bool {
	cfg := m.LoadSMTPConfig()
	channelConfigs := m.LoadNotifyChannels()
	for _, channel := range channels {
		if channel == "mail" {
			if cfg.Host != "" && cfg.From != "" {
				return true
			}
			continue
		}
		if item, ok := channelConfigs[channel]; ok && NotifyChannelConfigEnabled(item) {
			return true
		}
	}
	return false
}

// SnapshotSMTPConfig 把当前 SMTP 配置转换为可持久化的快照。
func SnapshotSMTPConfig(cfg SMTPConfig) *model.SMTPConfigSnapshot {
	return &model.SMTPConfigSnapshot{
		Host:     cfg.Host,
		Port:     cfg.Port,
		Username: cfg.Username,
		Password: cfg.Password,
		From:     cfg.From,
		SSL:      cfg.SSL,
	}
}

func smtpConfigFromSnapshot(cfg *model.SMTPConfigSnapshot) SMTPConfig {
	if cfg == nil {
		return SMTPConfig{}
	}
	return SMTPConfig{
		Host:     cfg.Host,
		Port:     cfg.Port,
		Username: cfg.Username,
		Password: cfg.Password,
		From:     cfg.From,
		SSL:      cfg.SSL,
	}
}

func (m *Manager) sendSecurityMail(cfg SMTPConfig, to, subject, body string) error {
	if m.mailSender != nil {
		return m.mailSender(cfg, to, subject, body)
	}
	return SendMail(cfg, to, subject, body)
}

func (m *Manager) sendSecurityChannel(channel string, cfg NotifyChannelConfig, subject, body string) error {
	if m.channelSender != nil {
		return m.channelSender(channel, cfg, subject, body)
	}
	return SendNotifyChannel(channel, cfg, subject, body)
}

func securityChannelKey(channel string) string {
	return "channel:" + strings.ToLower(strings.TrimSpace(channel))
}

func (m *Manager) finishSecurityEvent(event *model.SecurityEvent, attempts int, reason string) {
	_ = m.Store.UpdateSecurityEventDelivery(event.ID, event.Deliveries, attempts, reason, nil, nil)
	m.Store.AddLogKey("notify", event.UserID, "error", "log.security.notify_failed", map[string]any{
		"kind":  event.Kind,
		"error": reason,
	})
}

func securityRetryDelay(attempts int) time.Duration {
	if attempts <= 1 {
		return securityNotifyRetryBase
	}
	return securityNotifyRetryBase + time.Duration(attempts-1)*securityNotifyRetryStep
}

func securityDeliveryKey(recipient string) string {
	return "mail:" + strings.ToLower(strings.TrimSpace(recipient))
}

func (m *Manager) renderSecurityEmail(event model.SecurityEvent) (string, string) {
	locale := m.Locale()
	variant := strings.TrimSpace(event.Detail["variant"])
	titleKey := "email.security.title." + event.Kind
	if variant != "" {
		variantKey := titleKey + "." + variant
		if i18n.T(locale, variantKey, nil) != variantKey {
			titleKey = variantKey
		}
	}
	title := i18n.T(locale, titleKey, nil)
	if title == titleKey {
		title = i18n.T(locale, "email.security.title.default", nil)
	}
	subject := i18n.T(locale, "email.security.subject", map[string]any{"title": title})
	account := strings.TrimSpace(event.AccountEmail)
	if account == "" {
		account = i18n.T(locale, "email.security.unknown_account", nil)
	}
	ip := strings.TrimSpace(event.IP)
	if ip == "" {
		ip = i18n.T(locale, "email.security.unknown_ip", nil)
	}
	device := strings.TrimSpace(event.UserAgent)
	if device == "" {
		device = i18n.T(locale, "email.security.unknown_device", nil)
	}
	detailKey := "email.security.detail." + event.Kind
	if variant != "" {
		variantKey := detailKey + "." + variant
		if i18n.T(locale, variantKey, nil) != variantKey {
			detailKey = variantKey
		}
	}
	params := securityDetailParams(event)
	if value, _ := params["ips"].(string); value == "" {
		params["ips"] = i18n.T(locale, "email.security.unknown_ip", nil)
	}
	detail := i18n.Th(locale, detailKey, params)
	if detail == detailKey {
		detail = i18n.Th(locale, "email.security.detail.default", nil)
	}
	detail = strings.ReplaceAll(detail, "\n", "<br>")
	body := i18n.Th(locale, "email.security.layout", map[string]any{
		"title":   title,
		"time":    event.CreatedAt.Local().Format("2006-01-02 15:04:05 -07:00"),
		"account": account,
		"ip":      ip,
		"device":  device,
	})
	body = strings.ReplaceAll(body, "{detail}", detail)
	if link := strings.TrimSpace(event.Link); link != "" {
		body += i18n.Th(locale, "email.security.link", map[string]any{"link": link})
	}
	return subject, body
}

func securityDetailParams(event model.SecurityEvent) map[string]any {
	params := make(map[string]any, len(event.Detail)+4)
	for key, value := range event.Detail {
		params[key] = value
	}
	for _, key := range []string{"sessions", "api_keys", "passkeys", "remaining"} {
		if _, exists := params[key]; !exists {
			params[key] = "0"
		}
	}
	for _, key := range []string{"name", "prefix", "old_email", "new_email", "changes", "request_id", "count", "window", "ips", "limit"} {
		if _, exists := params[key]; !exists {
			params[key] = ""
		}
	}
	return params
}

func normalizeSecurityEvents(events []string) []string {
	seen := make(map[string]bool, len(events))
	out := make([]string, 0, len(events))
	for _, item := range events {
		value := strings.TrimSpace(item)
		if value == "" || seen[value] || !model.IsSecurityNotifyEventKind(value) {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func normalizeSecurityLoginMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off":
		return "off"
	case "always":
		return "always"
	case "new":
		return "new"
	default:
		return "new"
	}
}

func isSecurityLoginMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off", "new", "always":
		return true
	default:
		return false
	}
}

func normalizeSecurityChannels(channels []string) []string {
	seen := make(map[string]bool, len(channels))
	out := make([]string, 0, len(channels))
	for _, item := range channels {
		value := strings.ToLower(strings.TrimSpace(item))
		if value == "" || seen[value] || !IsNotifyChannel(value) {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) == 0 {
		return []string{"mail"}
	}
	return out
}

func validateSecurityChannels(channels []string) ([]string, error) {
	if len(channels) == 0 {
		return []string{"mail"}, nil
	}
	for _, item := range channels {
		value := strings.ToLower(strings.TrimSpace(item))
		if value == "" {
			continue
		}
		if !IsNotifyChannel(value) {
			return nil, i18n.E("api.error.security_notify_channel_invalid", "channel", value)
		}
	}
	return normalizeSecurityChannels(channels), nil
}

func parseSecurityRecipients(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if len(part) > 320 {
			return nil, i18n.E("api.error.security_notify_recipient_invalid", "email", part)
		}
		address, err := mail.ParseAddress(part)
		if err != nil || !strings.EqualFold(address.Address, part) || !strings.Contains(part, ".") {
			return nil, i18n.E("api.error.security_notify_recipient_invalid", "email", part)
		}
		out = append(out, strings.ToLower(part))
	}
	out = dedupeSecurityRecipients(out)
	if len(out) > securityNotifyMaxRecipients {
		return nil, i18n.E("api.error.security_notify_recipient_too_many", "max", securityNotifyMaxRecipients)
	}
	return out, nil
}

func canonicalSecurityRecipients(value string) (string, error) {
	items, err := parseSecurityRecipients(value)
	if err != nil {
		return "", err
	}
	return strings.Join(items, ","), nil
}

func dedupeSecurityRecipients(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.ToLower(strings.TrimSpace(item))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func securityRecipientKey(value string) string {
	items, err := parseSecurityRecipients(value)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(value))
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

func equalStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, item := range left {
		seen[item]++
	}
	for _, item := range right {
		if seen[item] == 0 {
			return false
		}
		seen[item]--
	}
	return true
}

func clampSecurityFailedThreshold(value int) int {
	if value == 0 {
		return securityNotifyDefaultFailedThreshold
	}
	if value < securityNotifyMinFailedThreshold {
		return securityNotifyMinFailedThreshold
	}
	if value > securityNotifyMaxFailedThreshold {
		return securityNotifyMaxFailedThreshold
	}
	return value
}

func clampSecurityFailedWindow(value int) int {
	if value == 0 {
		return securityNotifyDefaultFailedWindow
	}
	if value < securityNotifyMinFailedWindow {
		return securityNotifyMinFailedWindow
	}
	if value > securityNotifyMaxFailedWindow {
		return securityNotifyMaxFailedWindow
	}
	return value
}

func clampSecurityMaxPerHour(value int) int {
	if value == 0 {
		return securityNotifyDefaultMaxPerHour
	}
	if value < securityNotifyMinMaxPerHour {
		return securityNotifyMinMaxPerHour
	}
	if value > securityNotifyMaxMaxPerHour {
		return securityNotifyMaxMaxPerHour
	}
	return value
}

func clampSecurityNewDeviceDays(value int) int {
	if value == 0 {
		return securityNotifyDefaultNewDeviceDays
	}
	if value < securityNotifyMinNewDeviceDays {
		return securityNotifyMinNewDeviceDays
	}
	if value > securityNotifyMaxNewDeviceDays {
		return securityNotifyMaxNewDeviceDays
	}
	return value
}

func clampSecurityRetention(days int) int {
	if days < securityNotifyMinRetentionDays || days > securityNotifyMaxRetentionDays {
		if days == 0 {
			return securityNotifyDefaultRetentionDays
		}
		if days < securityNotifyMinRetentionDays {
			return securityNotifyMinRetentionDays
		}
		return securityNotifyMaxRetentionDays
	}
	return days
}

func sanitizeSecurityText(value string, max int) string {
	value = strings.Map(func(r rune) rune {
		if r >= 32 {
			return r
		}
		return -1
	}, value)
	value = strings.TrimSpace(value)
	if max > 0 && utf8.RuneCountInString(value) > max {
		value = string([]rune(value)[:max])
	}
	return value
}
