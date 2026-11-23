// Package doge 通过多吉云 API 上传证书并绑定 CDN 域名。
//
// 对应 certd 的「多吉云-部署到多吉云CDN」插件。
package doge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	deploy "lactssl/internal/plugin/deploy"
	"lactssl/internal/plugin/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "doge" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployDoge(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

var dogeBaseURL = "https://api.dogecloud.com"

type envelope struct {
	Code *int            `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func deployDoge(cert *deploy.NativeCertificate, cfg map[string]string) error {
	accessKey := strings.TrimSpace(cfg["access_key"])
	secretKey := cfg["secret_key"]
	if accessKey == "" || secretKey == "" {
		return i18n.E("service.deploy.doge_key_required")
	}
	domains := util.SplitList(cfg["domains"])
	if len(domains) == 0 {
		domains = util.Domains(cert)
	}
	if len(domains) == 0 {
		return i18n.E("service.deploy.cdn_domains_empty")
	}

	name := strings.TrimSpace(cfg["cert_name"])
	if name == "" {
		name = util.CertName(cert, "lactssl-"+util.CleanName(util.FirstDomain(cert)))
	}
	data, err := dogeRequest(accessKey, secretKey, "/cdn/cert/upload.json", map[string]string{
		"note":    name,
		"cert":    cert.FullchainPEM,
		"private": cert.KeyPEM,
	}, false)
	if err != nil {
		return err
	}
	var uploaded struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &uploaded); err != nil || uploaded.ID <= 0 {
		return i18n.E("service.deploy.doge_cert_id_missing")
	}

	ignore := util.Bool(cfg["ignore_error"])
	var errs []string
	for _, domain := range domains {
		_, err := dogeRequest(accessKey, secretKey, "/cdn/cert/bind.json", map[string]string{
			"id":     strconv.FormatInt(uploaded.ID, 10),
			"domain": domain,
		}, ignore)
		if err != nil {
			errs = append(errs, domain+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func dogeRequest(accessKey, secretKey, apiPath string, params map[string]string, ignoreNullCode bool) (json.RawMessage, error) {
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	body := values.Encode()
	mac := hmac.New(sha1.New, []byte(secretKey))
	mac.Write([]byte(apiPath + "\n" + body))
	authorization := "TOKEN " + accessKey + ":" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, dogeBaseURL+apiPath, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", authorization)
	client := outbound.NewClient(30*time.Second, false)
	resp, err := client.Do(req)
	if err != nil {
		return nil, i18n.E("service.deploy.doge_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result envelope
	_ = json.Unmarshal(raw, &result)
	if result.Code == nil && ignoreNullCode {
		return result.Data, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (result.Code != nil && *result.Code != 200) {
		if result.Msg != "" {
			return nil, i18n.E("service.deploy.doge_error", "message", result.Msg)
		}
		return nil, i18n.E("service.deploy.doge_status", "status", resp.StatusCode, "body", util.Truncate(string(raw), 300))
	}
	return result.Data, nil
}
