package auth

import (
	"encoding/json"
	"strconv"

	"github.com/go-webauthn/webauthn/webauthn"
	"lactssl/internal/model"
)

// WebAuthnUser 把本地账户与通行密钥适配为 WebAuthn 库所需的用户接口。
type WebAuthnUser struct {
	User     *model.User
	Passkeys []model.Passkey
}

// NewWebAuthnUser 创建 WebAuthn 用户适配器。
func NewWebAuthnUser(user *model.User, passkeys []model.Passkey) *WebAuthnUser {
	return &WebAuthnUser{User: user, Passkeys: passkeys}
}

// WebAuthnID 返回稳定的用户句柄。用户句柄不能包含个人信息，因此使用账户 ID。
func (u *WebAuthnUser) WebAuthnID() []byte {
	return []byte(strconv.FormatInt(u.User.ID, 10))
}

// WebAuthnName 返回 WebAuthn 展示名称（登录名）。
func (u *WebAuthnUser) WebAuthnName() string {
	return u.User.Email
}

// WebAuthnDisplayName 返回 WebAuthn 展示名称。
func (u *WebAuthnUser) WebAuthnDisplayName() string {
	return u.User.Email
}

// WebAuthnCredentials 返回账户下可用于认证的凭据列表。
func (u *WebAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return DecodePasskeyCredentials(u.Passkeys)
}

// DecodePasskeyCredentials 解码数据库中保存的 WebAuthn 凭据。
// 单条损坏的凭据会被跳过，避免影响其他通行密钥登录。
func DecodePasskeyCredentials(passkeys []model.Passkey) []webauthn.Credential {
	credentials := make([]webauthn.Credential, 0, len(passkeys))
	for _, item := range passkeys {
		if item.Credential == "" {
			continue
		}
		var credential webauthn.Credential
		if err := json.Unmarshal([]byte(item.Credential), &credential); err != nil {
			continue
		}
		credentials = append(credentials, credential)
	}
	return credentials
}

// EncodePasskeyCredential 把 WebAuthn 凭据序列化为可落库的 JSON。
func EncodePasskeyCredential(credential *webauthn.Credential) (string, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
