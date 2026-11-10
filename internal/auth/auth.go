// Package auth 提供密码、JWT 与 TOTP 相关能力。
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"lactssl/internal/i18n"
	"lactssl/internal/model"
)

// Manager 负责签发与校验登录令牌。
type Manager struct {
	secret []byte
}

// Claims 是 JWT 载荷，带版本号以便改密后使旧令牌失效。
type Claims struct {
	UserID  int64 `json:"uid"`
	Version int   `json:"ver"`
	jwt.RegisteredClaims
}

// New 创建认证管理器。
func New(secret []byte) *Manager {
	return &Manager{secret: secret}
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
	signed, err := token.SignedString(m.secret)
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

// Parse 校验令牌并返回载荷。
func (m *Manager) Parse(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, i18n.E("api.error.unsupported_algorithm")
		}
		return m.secret, nil
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

// ValidateTOTP 校验动态验证码，允许前后各一个时间窗口。
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
	return err == nil && ok
}
