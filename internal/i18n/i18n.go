// Package i18n 提供后端多语言文案、语言协商与持久化消息的编解码。
//
// 设计目标：新增一门语言只需要在 locales 目录放入一个 <locale>.json 文件，
// 业务代码不需要任何改动。
package i18n

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// Locale 是 BCP 47 语言标签，例如 zh-CN、zh-TW、en-US、ja-JP。
type Locale string

// Default 是默认语言，也是翻译缺失时的回退语言。
const Default Locale = "zh-CN"

// 常见语言常量，便于调用方避免拼写错误。
const (
	ZhCN Locale = "zh-CN"
	ZhTW Locale = "zh-TW"
	EnUS Locale = "en-US"
	RuRU Locale = "ru-RU"
	JaJP Locale = "ja-JP"
)

// 消息前缀用于区分普通文本与结构化消息。
const messagePrefix = "@@i18n:"

// ContextKey 是 Gin 上下文中保存当前语言的键。
const ContextKey = "lactssl.locale"

//go:embed locales/*.json
var localeFS embed.FS

type catalog map[string]string

var (
	loadOnce  sync.Once
	catalogs  map[Locale]catalog
	languages map[string]Locale // 语言主标签 -> 具体 locale
	locales   []Locale
)

func load() {
	loadOnce.Do(func() {
		catalogs = map[Locale]catalog{}
		languages = map[string]Locale{}
		entries, err := localeFS.ReadDir("locales")
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			raw, err := localeFS.ReadFile("locales/" + entry.Name())
			if err != nil {
				continue
			}
			var values map[string]string
			if err := json.Unmarshal(raw, &values); err != nil {
				// 单个语言文件损坏时跳过，不影响其他语言。
				continue
			}
			locale := Locale(strings.TrimSuffix(entry.Name(), ".json"))
			catalogs[locale] = catalog(values)
			locales = append(locales, locale)
		}
		sort.Slice(locales, func(i, j int) bool { return locales[i] < locales[j] })
		// 只有一个具体地区时，语言主标签直接映射到它；多个地区时优先使用默认语言。
		byLanguage := map[string][]Locale{}
		for locale := range catalogs {
			base := baseLanguage(string(locale))
			byLanguage[base] = append(byLanguage[base], locale)
		}
		for base, list := range byLanguage {
			sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
			if len(list) == 1 {
				languages[base] = list[0]
				continue
			}
			languages[base] = list[0]
			if _, ok := catalogs[Default]; ok && baseLanguage(string(Default)) == base {
				languages[base] = Default
			}
		}
	})
}

// Supported 返回所有已注册的语言。
func Supported() []Locale {
	load()
	out := make([]Locale, len(locales))
	copy(out, locales)
	return out
}

// Has 判断某个语言是否已注册。
func Has(locale Locale) bool {
	load()
	_, ok := catalogs[locale]
	return ok
}

// Normalize 将任意语言标签归一化为受支持的 locale，无法识别时返回默认语言。
func Normalize(tag string) Locale {
	load()
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(tag, "_", "-")))
	if normalized == "" {
		return Default
	}
	// 先尝试完整标签（大小写不敏感）。
	for locale := range catalogs {
		if strings.ToLower(string(locale)) == normalized {
			return locale
		}
	}
	// 繁体中文的常见写法统一到 zh-TW，例如 zh-Hant、zh-HK、zh-MO。
	if isTraditionalChinese(normalized) {
		if _, ok := catalogs[ZhTW]; ok {
			return ZhTW
		}
	}
	// 再按语言主标签匹配，例如 en-GB -> en-US、zh -> zh-CN。
	if locale, ok := languages[baseLanguage(normalized)]; ok {
		return locale
	}
	return Default
}

// isTraditionalChinese 判断语言标签是否属于繁体中文。
func isTraditionalChinese(tag string) bool {
	return strings.HasPrefix(tag, "zh-hant") ||
		strings.HasPrefix(tag, "zh-tw") ||
		strings.HasPrefix(tag, "zh-hk") ||
		strings.HasPrefix(tag, "zh-mo")
}

// ParseAcceptLanguage 解析 Accept-Language，返回第一个受支持的语言。
func ParseAcceptLanguage(header string) Locale {
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if tag == "" || tag == "*" {
			continue
		}
		load()
		// 先精确匹配，避免 zh-TW 被同主标签的 zh-CN 抢先命中。
		for locale := range catalogs {
			if strings.EqualFold(string(locale), tag) {
				return locale
			}
		}
		if isTraditionalChinese(strings.ToLower(tag)) {
			if _, ok := catalogs[ZhTW]; ok {
				return ZhTW
			}
		}
		// 客户端只给了语言主标签时按主标签匹配，例如 en -> en-US、zh -> zh-CN。
		for locale := range catalogs {
			if strings.EqualFold(baseLanguage(string(locale)), baseLanguage(tag)) {
				return locale
			}
		}
	}
	return Default
}

// FromRequest 依次从自定义头、Accept-Language 中协商语言。
func FromRequest(r *http.Request) Locale {
	if r == nil {
		return Default
	}
	if tag := strings.TrimSpace(r.Header.Get("X-LACTSSL-Language")); tag != "" {
		return Normalize(tag)
	}
	return ParseAcceptLanguage(r.Header.Get("Accept-Language"))
}

// FromContext 返回当前请求的语言。
func FromContext(c *gin.Context) Locale {
	if c == nil {
		return Default
	}
	if value, ok := c.Get(ContextKey); ok {
		if locale, ok := value.(Locale); ok {
			return locale
		}
		if text, ok := value.(string); ok {
			return Normalize(text)
		}
	}
	return FromRequest(c.Request)
}

// T 翻译指定 key。params 使用 {name} 占位符替换。
func T(locale Locale, key string, params map[string]any) string {
	load()
	text := translate(locale, key)
	if text == "" {
		return key
	}
	for name, value := range params {
		text = strings.ReplaceAll(text, "{"+name+"}", fmt.Sprint(value))
	}
	return text
}

// Th 与 T 相同，但对参数做 HTML 转义，用于拼接邮件等 HTML 内容。
func Th(locale Locale, key string, params map[string]any) string {
	load()
	text := translate(locale, key)
	if text == "" {
		return key
	}
	for name, value := range params {
		text = strings.ReplaceAll(text, "{"+name+"}", html.EscapeString(fmt.Sprint(value)))
	}
	return text
}

// Tf 是 T 的便捷写法，参数按 key/value 交替传入。
func Tf(locale Locale, key string, pairs ...any) string {
	params := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		name, ok := pairs[i].(string)
		if !ok {
			continue
		}
		params[name] = pairs[i+1]
	}
	return T(locale, key, params)
}

func lookup(locale Locale, key string) string {
	if values, ok := catalogs[locale]; ok {
		if text, ok := values[key]; ok {
			return text
		}
	}
	return ""
}

// fallbackChain 为非默认回退语言指定回退顺序。
// 例如俄语、日语缺译时先显示英文，再回退默认语言；繁体中文缺译时直接回退默认语言。
var fallbackChain = map[Locale][]Locale{
	ZhTW: {Default},
	RuRU: {EnUS, Default},
	JaJP: {EnUS, Default},
}

func translate(locale Locale, key string) string {
	if text := lookup(locale, key); text != "" {
		return text
	}
	for _, fallback := range fallbackChain[locale] {
		if text := lookup(fallback, key); text != "" {
			return text
		}
	}
	if locale != Default {
		// 其余语言仍然回退到默认语言；仍然缺失时由调用方返回 key。
		return lookup(Default, key)
	}
	return ""
}

func baseLanguage(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if idx := strings.IndexAny(tag, "-_"); idx >= 0 {
		return tag[:idx]
	}
	return tag
}

// ---------------------------- 持久化消息 ----------------------------

type encodedMessage struct {
	Key    string         `json:"key"`
	Params map[string]any `json:"params,omitempty"`
}

// Encode 把消息编码为可持久化的结构化文本。
// 数据库字段仍然是一个 TEXT，但内容与语言无关，渲染时按当前语言翻译。
func Encode(key string, params map[string]any) string {
	raw, err := json.Marshal(encodedMessage{Key: key, Params: params})
	if err != nil {
		return key
	}
	return messagePrefix + string(raw)
}

// Decode 判断并解析结构化消息。
func Decode(text string) (key string, params map[string]any, ok bool) {
	if !strings.HasPrefix(text, messagePrefix) {
		return "", nil, false
	}
	var message encodedMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(text, messagePrefix)), &message); err != nil || message.Key == "" {
		return "", nil, false
	}
	return message.Key, message.Params, true
}

// Render 把持久化消息按指定语言渲染；普通文本原样返回，兼容历史数据。
func Render(locale Locale, text string) string {
	return render(locale, text, 0)
}

func render(locale Locale, text string, depth int) string {
	if key, params, ok := Decode(text); ok {
		return T(locale, key, params)
	}
	if locale == Default || depth >= 3 {
		return text
	}
	// i18n 改造前的历史数据是默认语言原文，尝试按模板反向匹配后翻译。
	key, params, ok := matchLegacy(text)
	if !ok {
		return text
	}
	// 嵌套消息（例如监控日志里的 message 参数）继续翻译。
	for name, value := range params {
		if nested, ok := value.(string); ok && nested != text {
			params[name] = render(locale, nested, depth+1)
		}
	}
	return T(locale, key, params)
}

// Error 是带 key 的错误，便于在 API 层按请求语言翻译。
type Error struct {
	Key    string
	Params map[string]any
	Raw    string
	// Cause 保留底层错误链，便于调用方用 errors.Is/errors.As 做类型判断，
	// 同时仍然按 Key 给用户展示可翻译消息。
	Cause error
}

// Error 返回默认语言文本，满足 error 接口。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Key != "" {
		return T(Default, e.Key, e.Params)
	}
	return e.Raw
}

// E 构造一个可翻译错误。
func E(key string, pairs ...any) error {
	params := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		name, ok := pairs[i].(string)
		if !ok {
			continue
		}
		params[name] = pairs[i+1]
	}
	return &Error{Key: key, Params: params}
}

// Unwrap 返回底层错误，供 errors.Is/errors.As 穿透包装。
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Wrap 构造带底层错误的可翻译错误。
// 与 E 的区别是保留原始错误链，适合 ACME 上游错误分类等场景。
func Wrap(key string, cause error, pairs ...any) error {
	params := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		name, ok := pairs[i].(string)
		if !ok {
			continue
		}
		params[name] = pairs[i+1]
	}
	return &Error{Key: key, Params: params, Cause: cause}
}

// EF 使用 fmt.Errorf 的格式构造原始错误，保持底层错误链。
func EF(format string, args ...any) error {
	return &Error{Raw: fmt.Sprintf(format, args...)}
}

// LocalizedError 允许包装底层错误的同时提供可翻译的消息。
// 例如服务层需要在保留 ACME 原始错误链（供 errors.As 判断）的前提下，
// 仍然把错误按当前语言持久化和展示。
type LocalizedError interface {
	error
	LocalizedMessage() (key string, params map[string]any)
}

// EncodeError 把错误转换为可持久化的消息：可翻译错误保存 key，其他错误保存原文。
func EncodeError(err error) string {
	if err == nil {
		return ""
	}
	var localized LocalizedError
	if errors.As(err, &localized) {
		if key, params := localized.LocalizedMessage(); key != "" {
			return Encode(key, params)
		}
	}
	var keyed *Error
	if errors.As(err, &keyed) && keyed.Key != "" {
		return Encode(keyed.Key, keyed.Params)
	}
	return err.Error()
}

// ErrorText 按指定语言渲染错误。
func ErrorText(locale Locale, err error) string {
	if err == nil {
		return ""
	}
	var localized LocalizedError
	if errors.As(err, &localized) {
		if key, params := localized.LocalizedMessage(); key != "" {
			return T(locale, key, params)
		}
	}
	var keyed *Error
	if errors.As(err, &keyed) && keyed.Key != "" {
		return T(locale, keyed.Key, keyed.Params)
	}
	return err.Error()
}
