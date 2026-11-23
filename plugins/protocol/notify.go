package protocol

// NotifyManifest 是通知 helper 通过 `manifest` 子命令输出的协议描述。
type NotifyManifest struct {
	Protocol string `json:"protocol"`
	Name     string `json:"name,omitempty"`
	Version  string `json:"version,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	// Channels 是该 helper 支持的通知渠道列表。
	Channels []NotifyChannel `json:"channels"`
}

// NotifyChannel 描述一个由 helper 提供的通知渠道。
type NotifyChannel struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	// Fields 是渠道配置字段；secret 或 password 字段会被脱敏并加密保存。
	Fields []NotifyField `json:"fields,omitempty"`
}

// NotifyField 描述通知渠道的配置字段。
// LabelKey 供官方前端做本地化；第三方插件可以直接使用 Label。
type NotifyField struct {
	Key         string         `json:"key"`
	Label       string         `json:"label,omitempty"`
	LabelKey    string         `json:"label_key,omitempty"`
	Type        string         `json:"type,omitempty"`
	Required    bool           `json:"required,omitempty"`
	Secret      bool           `json:"secret,omitempty"`
	Placeholder string         `json:"placeholder,omitempty"`
	Help        string         `json:"help,omitempty"`
	Default     string         `json:"default,omitempty"`
	Options     []NotifyOption `json:"options,omitempty"`
}

// NotifyOption 是下拉选项。
type NotifyOption struct {
	Label    string `json:"label,omitempty"`
	LabelKey string `json:"label_key,omitempty"`
	Value    string `json:"value"`
}

// NotifyRequest 是 send 的 stdin 请求。
type NotifyRequest struct {
	Action   string         `json:"action"`
	Protocol string         `json:"protocol,omitempty"`
	Channel  string         `json:"channel"`
	Config   map[string]any `json:"config,omitempty"`
	Title    string         `json:"title,omitempty"`
	Body     string         `json:"body,omitempty"`
}
