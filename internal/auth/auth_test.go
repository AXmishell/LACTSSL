package auth

import (
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp/totp"
	"lactssl/internal/model"
)

func TestPasswordAndJWT(t *testing.T) {
	hash, err := HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "password123") {
		t.Fatal("密码校验失败")
	}
	if VerifyPassword(hash, "wrong-password") {
		t.Fatal("错误密码不应通过校验")
	}

	manager := New([]byte("jwt-secret"))
	user := &model.User{ID: 7, TokenVersion: 3}
	token, _, err := manager.Issue(user)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := manager.Parse(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != 7 || claims.Version != 3 {
		t.Fatalf("令牌载荷不正确: %+v", claims)
	}
}

// TestBootScopedManagerInvalidatesTokens 验证启动级签名密钥会让旧令牌失效，
// 同时门禁/CSRF 等基于原始 secret 的短期签名跨重启仍然有效。
func TestBootScopedManagerInvalidatesTokens(t *testing.T) {
	first, err := NewBootScoped([]byte("jwt-secret"))
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := first.Issue(&model.User{ID: 7, TokenVersion: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Parse(token); err != nil {
		t.Fatalf("同一进程内令牌应通过校验: %v", err)
	}

	second, err := NewBootScoped([]byte("jwt-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Parse(token); err == nil {
		t.Fatal("重启后旧令牌不应继续通过校验")
	}

	signature := first.SignValue("entry-gate", "my-entry")
	if !second.VerifyValue("entry-gate", "my-entry", signature) {
		t.Fatal("门禁签名不应随启动轮换而失效")
	}
}

func TestSignValue(t *testing.T) {
	manager := New([]byte("jwt-secret"))
	signature := manager.SignValue("entry-gate", "my-entry")
	if !manager.VerifyValue("entry-gate", "my-entry", signature) {
		t.Fatal("正确的门禁签名未通过校验")
	}
	if manager.VerifyValue("entry-gate", "other-entry", signature) {
		t.Fatal("不同取值的签名不应通过校验")
	}
	if manager.VerifyValue("other-purpose", "my-entry", signature) {
		t.Fatal("不同用途的签名不应通过校验")
	}
	if New([]byte("other-secret")).VerifyValue("entry-gate", "my-entry", signature) {
		t.Fatal("不同密钥的签名不应通过校验")
	}
}

func TestTOTP(t *testing.T) {
	secret, url, err := GenerateTOTP("admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || url == "" {
		t.Fatal("TOTP 密钥生成失败")
	}
	code, err := totp.GenerateCode(secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !ValidateTOTP(secret, code) {
		t.Fatal("正确的动态验证码未通过校验")
	}
	if ValidateTOTP(secret, code) {
		t.Fatal("同一动态验证码不应在有效窗口内重复使用")
	}
	if ValidateTOTP(secret, "000000") {
		t.Fatal("错误的动态验证码不应通过校验")
	}
}

func TestTOTPRejectsConcurrentReplay(t *testing.T) {
	secret, _, err := GenerateTOTP("race@example.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	const workers = 64
	var wg sync.WaitGroup
	results := make([]bool, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index] = ValidateTOTP(secret, code)
		}(i)
	}
	close(start)
	wg.Wait()
	accepted := 0
	for _, ok := range results {
		if ok {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("同一 TOTP 验证码并发校验成功 %d 次，期望恰好 1 次", accepted)
	}
}

func TestExpiringValue(t *testing.T) {
	manager := New([]byte("jwt-secret"))
	valid := manager.SignExpiringValue("entry-gate", "my-entry", time.Now().Add(time.Minute))
	if !manager.VerifyExpiringValue("entry-gate", "my-entry", valid) {
		t.Fatal("未过期的签名值应通过校验")
	}
	expired := manager.SignExpiringValue("entry-gate", "my-entry", time.Now().Add(-time.Minute))
	if manager.VerifyExpiringValue("entry-gate", "my-entry", expired) {
		t.Fatal("已过期的签名值不应通过校验")
	}
	if manager.VerifyExpiringValue("other-purpose", "my-entry", valid) {
		t.Fatal("不同用途的签名值不应通过校验")
	}
}

func TestJWTSigningUsesDerivedKey(t *testing.T) {
	secret := []byte("jwt-secret")
	manager := New(secret)
	claims := Claims{
		UserID:  1,
		Version: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "forged",
			Subject:   "1",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			Issuer:    "LACTSSL",
		},
	}
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Parse(forged); err == nil {
		t.Fatal("使用原始 secret 直接签名的令牌不应被接受")
	}
}
