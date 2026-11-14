// Package auth 提供密码、JWT 与 TOTP 相关能力。
package auth

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// Manager 负责签发与校验登录令牌。
//
// secret 同时用于 JWT 与一次性门禁值的 HMAC；两者使用不同的派生密钥，
// 避免与数据库 AES-GCM 主密钥（旧版本可能复用同一串）发生密钥复用。
type Manager struct {
	secret  []byte // 原始密钥，用于门禁/CSRF 等 purpose HMAC
	signKey []byte // 由 secret 派生的 JWT 专用签名密钥
}

// Claims 是 JWT 载荷，带版本号以便改密后使旧令牌失效。
type Claims struct {
	UserID  int64 `json:"uid"`
	Version int   `json:"ver"`
	jwt.RegisteredClaims
}

// New 创建认证管理器。JWT 使用 HKDF 派生的独立子密钥，
// 即使旧配置中 jwt_secret 与 salt 相同，也不会让 JWT 与数据库加密共享同一把密钥。
func New(secret []byte) *Manager {
	return newManager(secret, nil)
}

// NewBootScoped 创建进程级认证管理器：把本次启动生成的随机值混入
// JWT 签名子密钥，使进程重启后旧登录令牌全部无法通过签名校验。
//
// 随机值只参与 JWT 签名，不参与门禁、CSRF 和二次验证的 HMAC，
// 避免重启把这些短期凭证一起作废。调用方仍需清理 sessions 表，
// 否则设置页会继续显示实际已不可用的历史会话。
func NewBootScoped(secret []byte) (*Manager, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return newManager(secret, nonce), nil
}

// newManager 使用指定 salt 派生 JWT 签名子密钥；nonce 为空时保持旧版行为。
func newManager(secret, nonce []byte) *Manager {
	signKey, err := hkdf.Key(sha256.New, secret, nonce, "lactssl/jwt-signing-v1", 32)
	if err != nil || len(signKey) == 0 {
		// hkdf 仅在参数非法时失败；回退到原始密钥，保证服务可用。
		signKey = secret
	}
	return &Manager{secret: secret, signKey: signKey}
}

var dummyHash string

func init() {
	hash, _ := bcrypt.GenerateFromPassword([]byte("lactssl-dummy-password"), bcrypt.DefaultCost)
	dummyHash = string(hash)
}

// VerifyDummy 执行一次等时 bcrypt 校验，用于用户不存在时降低邮箱枚举风险。
func VerifyDummy(password string) {
	_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
}

// HashPassword 生成 bcrypt 密码摘要。
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

// VerifyPassword 校验密码。
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Issue 为账户签发 7 天有效的令牌。
func (m *Manager) Issue(user *model.User) (string, time.Time, error) {
	token, expire, _, err := m.IssueSession(user)
	return token, expire, err
}

// IssueSession 签发令牌并返回会话 ID，调用方据此登记服务端会话。
func (m *Manager) IssueSession(user *model.User) (string, time.Time, string, error) {
	expire := time.Now().Add(7 * 24 * time.Hour)
	sessionID, err := newSessionID()
	if err != nil {
		return "", time.Time{}, "", err
	}
	claims := Claims{
		UserID:  user.ID,
		Version: user.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        sessionID,
			Subject:   strconv.FormatInt(user.ID, 10),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expire),
			Issuer:    "LACTSSL",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.signKey)
	return signed, expire, sessionID, err
}

// newSessionID 生成不可预测的会话标识。
func newSessionID() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// SignValue 用服务端密钥为一次性门禁值生成 HMAC 签名。
func (m *Manager) SignValue(purpose, value string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(purpose))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyValue 校验门禁值签名，使用常量时间比较。
func (m *Manager) VerifyValue(purpose, value, signature string) bool {
	expected := m.SignValue(purpose, value)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

// SignExpiringValue 生成带服务端过期时间的签名值，格式为 <unix>.<hmac>。
// 用于入口门禁 Cookie，避免仅依赖浏览器端过期时间。
func (m *Manager) SignExpiringValue(purpose, value string, expires time.Time) string {
	deadline := strconv.FormatInt(expires.Unix(), 10)
	return deadline + "." + m.SignValue(purpose, value+"|"+deadline)
}

// VerifyExpiringValue 校验带过期时间的签名值。
func (m *Manager) VerifyExpiringValue(purpose, value, token string) bool {
	deadline, signature, ok := strings.Cut(token, ".")
	if !ok || deadline == "" || signature == "" {
		return false
	}
	expires, err := strconv.ParseInt(deadline, 10, 64)
	if err != nil || !time.Now().Before(time.Unix(expires, 0)) {
		return false
	}
	expected := m.SignValue(purpose, value+"|"+deadline)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

// Parse 校验令牌并返回载荷。
func (m *Manager) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, i18n.E("api.error.unsupported_algorithm")
		}
		return m.signKey, nil
	}, jwt.WithIssuer("LACTSSL"), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return nil, i18n.E("api.error.session_expired")
	}
	if claims.UserID == 0 {
		return nil, i18n.E("api.error.token_incomplete")
	}
	return claims, nil
}

// GenerateTOTP 生成新的 TOTP 密钥和 otpauth 链接。
func GenerateTOTP(email string) (string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "LACTSSL",
		AccountName: email,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", i18n.E("api.error.totp_generate_failed", "error", err.Error())
	}
	return key.Secret(), key.URL(), nil
}

// TOTP 验证码在有效窗口内只允许使用一次，避免被重放。
var (
	totpReplay      sync.Map // "secret|code" -> 过期 Unix 秒
	totpReplaySweep atomic.Int64
)

// ValidateTOTP 校验动态验证码，允许前后各一个时间窗口，并拒绝窗口内重复使用。
func ValidateTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if secret == "" || code == "" {
		return false
	}
	ok, err := totp.ValidateCustom(code, secret, time.Now().UTC(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil || !ok {
		return false
	}
	now := time.Now()
	key := secret + "|" + code
	expires := now.Add(2 * time.Minute).Unix()
	// LoadOrStore 原子抢占验证码。仅靠 Load + Store 会让并发请求同时通过，
	// 使同一个 TOTP 验证码被使用多次。
	if value, loaded := totpReplay.LoadOrStore(key, expires); loaded {
		if old, valid := value.(int64); valid && old > now.Unix() {
			return false
		}
		// 过期条目：用 CompareAndSwap 抢占，避免多个请求同时覆盖后都成功。
		if !totpReplay.CompareAndSwap(key, value, expires) {
			return false
		}
	}
	sweepTOTPReplay(now)
	return true
}

// sweepTOTPReplay 定期清理过期条目，避免长期运行后内存增长。
func sweepTOTPReplay(now time.Time) {
	last := totpReplaySweep.Load()
	if last != 0 && now.Unix()-last < 300 {
		return
	}
	if !totpReplaySweep.CompareAndSwap(last, now.Unix()) {
		return
	}
	totpReplay.Range(func(key, value any) bool {
		if expires, ok := value.(int64); !ok || expires <= now.Unix() {
			totpReplay.Delete(key)
		}
		return true
	})
}
