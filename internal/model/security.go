package model

import (
	"strings"
	"time"
)

// 安全通知事件类型。事件类型一旦发布只增不改，前端策略、i18n 文案和测试
// 都依赖这些稳定字符串。
const (
	SecurityEventPasswordChanged = "credential.password.changed"
	SecurityEventEmailChanged    = "credential.email.changed"
	SecurityEventTOTPEnabled     = "credential.totp.enabled"
	SecurityEventTOTPDisabled    = "credential.totp.disabled"
	SecurityEventPasskeyAdded    = "credential.passkey.added"
	SecurityEventPasskeyDeleted  = "credential.passkey.deleted"
	SecurityEventPasswordReset   = "credential.recovery.reset"

	SecurityEventAPIKeyCreated = "apikey.created"
	SecurityEventAPIKeyUpdated = "apikey.updated"
	SecurityEventAPIKeyDeleted = "apikey.deleted"
	SecurityEventAPIKeyRotated = "apikey.rotated"

	SecurityEventNotifyChanged = "settings.notify.changed"

	// 登录与测试事件不进入核心凭据事件开关，由独立的登录策略或测试入口控制。
	SecurityEventLoginPassword = "login.password"
	SecurityEventLoginPasskey  = "login.passkey"
	SecurityEventLoginFailed   = "login.failed"
	SecurityEventTest          = "security.test"
	SecurityEventDigest        = "security.digest"
)

// SMTPConfigSnapshot 是通知目标变更时使用的旧 SMTP 配置快照。
// 落库前由 Store 用数据库密钥加密，避免明文保存 SMTP 口令。
type SMTPConfigSnapshot struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	SSL      bool   `json:"ssl"`
}

// SecurityNotifyEventKinds 返回阶段 0 可配置的核心凭据事件。
// 新增事件时必须同步前端策略选项和后端 i18n 文案。
func SecurityNotifyEventKinds() []string {
	return []string{
		SecurityEventPasswordChanged,
		SecurityEventEmailChanged,
		SecurityEventTOTPEnabled,
		SecurityEventTOTPDisabled,
		SecurityEventPasskeyAdded,
		SecurityEventPasskeyDeleted,
		SecurityEventPasswordReset,
		SecurityEventAPIKeyCreated,
		SecurityEventAPIKeyUpdated,
		SecurityEventAPIKeyDeleted,
		SecurityEventAPIKeyRotated,
		SecurityEventNotifyChanged,
	}
}

// IsSecurityNotifyEventKind 判断事件是否属于可配置的核心凭据事件。
func IsSecurityNotifyEventKind(kind string) bool {
	for _, item := range SecurityNotifyEventKinds() {
		if item == kind {
			return true
		}
	}
	return false
}

// 安全事件投递状态。API 与前端统一使用这些稳定字符串，避免前端自己
// 根据 delivered_at/last_error 拼状态时把“已发送但结果写库失败”误判为待投递。
const (
	SecurityEventStateRecorded  = "recorded"
	SecurityEventStateDelivered = "delivered"
	SecurityEventStateFailed    = "failed"
	SecurityEventStateRetrying  = "retrying"
	SecurityEventStatePending   = "pending"
)

// SecurityEvent 记录一条安全事件，同时作为通知发送队列（outbox）。
// Detail 中的值会在写库前裁剪，渲染邮件时统一做 HTML 转义。
type SecurityEvent struct {
	ID          int64             `json:"id"`
	UserID      int64             `json:"user_id"`
	Kind        string            `json:"kind"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	IP          string            `json:"ip"`
	UserAgent   string            `json:"user_agent"`
	Detail      map[string]string `json:"detail"`
	// Recipients / Channels 是事件发生时的投递快照，避免之后修改策略影响历史事件。
	Recipients []string `json:"-"`
	Channels   []string `json:"-"`
	// AccountEmail 用于渲染邮件；空时由服务层按 UserID 查询。
	AccountEmail string `json:"-"`
	// Link 是可选的管理台地址，只允许由服务端 PublicURL 生成。
	Link string `json:"-"`
	// Force 绕过当前策略，用于“通知目标变更”这类必须告知旧收件人的事件。
	Force bool `json:"-"`
	// BypassRateLimit 用于必须绕开每小时上限的事件（密钥变更通知、测试、摘要）。
	BypassRateLimit bool `json:"-"`
	Notify          bool `json:"notify"`
	// SMTP 是事件发生时的旧 SMTP 配置快照；为空时使用当前设置。
	SMTP *SMTPConfigSnapshot `json:"-"`
	// Deliveries 记录每个目标的投递结果，重试时跳过已成功的收件人。
	Deliveries    map[string]string `json:"-"`
	Attempts      int               `json:"attempts"`
	LastError     string            `json:"last_error"`
	CreatedAt     time.Time         `json:"created_at"`
	NextAttemptAt time.Time         `json:"-"`
	DeliveredAt   *time.Time        `json:"delivered_at"`
}

// DeliveryState 计算事件当前的投递状态。
// 判定时以逐目标 deliveries 为准：即使 delivered_at 因历史版本写库失败而
// 为空，只要所有必需目标都已是 sent，也按已投递展示，避免“邮件已发但界面
// 一直显示待投递”。skipped 的插件渠道不算必需目标。
func (e *SecurityEvent) DeliveryState() string {
	if e == nil || !e.Notify {
		return SecurityEventStateRecorded
	}
	if e.DeliveredAt != nil {
		return SecurityEventStateDelivered
	}
	channels := e.Channels
	if len(channels) == 0 {
		// 历史事件可能没有持久化 channels；有收件人时按默认邮件渠道处理。
		channels = []string{"mail"}
	}
	expected := 0
	sent := 0
	failed := 0
	for _, channel := range channels {
		name := strings.ToLower(strings.TrimSpace(channel))
		if name == "" {
			continue
		}
		if name == "mail" {
			for _, recipient := range e.Recipients {
				key := "mail:" + strings.ToLower(strings.TrimSpace(recipient))
				if key == "mail:" {
					continue
				}
				expected++
				switch e.Deliveries[key] {
				case "sent":
					sent++
				case "failed":
					failed++
				}
			}
			continue
		}
		key := "channel:" + name
		state := e.Deliveries[key]
		if state == "skipped" {
			// 未启用的插件渠道不应阻止事件完成，服务层也把它排除在必需目标外。
			continue
		}
		expected++
		switch state {
		case "sent":
			sent++
		case "failed":
			failed++
		}
	}
	if expected > 0 && sent == expected {
		return SecurityEventStateDelivered
	}
	if expected == 0 {
		if e.Attempts > 0 || strings.TrimSpace(e.LastError) != "" {
			return SecurityEventStateFailed
		}
		return SecurityEventStatePending
	}
	if e.NextAttemptAt.IsZero() {
		return SecurityEventStateFailed
	}
	if failed > 0 || strings.TrimSpace(e.LastError) != "" {
		return SecurityEventStateRetrying
	}
	return SecurityEventStatePending
}
