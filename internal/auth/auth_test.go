package auth

import (
	"testing"
	"time"

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
	if ValidateTOTP(secret, "000000") {
		t.Fatal("错误的动态验证码不应通过校验")
	}
}
