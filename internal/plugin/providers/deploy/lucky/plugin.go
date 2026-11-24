// Package lucky 通过 Lucky 的 OpenToken API 更新已有证书。
//
// 对应 certd 的「lucky-更新Lucky证书」插件。
package lucky

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	deploy "lactssl/providers/api/deploy"
	"lactssl/providers/deploy/util"
)

type plugin struct{}

func (plugin) ID() string        { return "lucky" }
func (plugin) Aliases() []string { return nil }
func (plugin) Deploy(_ context.Context, req deploy.NativeRequest) error {
	return deployLucky(util.Certificate(req.Certificate), util.Merge(req.Auth, req.Config))
}

func init() { deploy.RegisterNative(plugin{}) }

type client struct {
	base      string
	safePath  string
	openToken string
	http      *http.Client
}

type response struct {
	Ret  *int            `json:"ret"`
	Msg  string          `json:"msg"`
	List []certificate   `json:"list"`
	Data json.RawMessage `json:"data"`
}

type certificate struct {
	Key       string          `json:"Key"`
	Remark    string          `json:"Remark"`
	CertsInfo json.RawMessage `json:"CertsInfo"`
}

type certsInfo struct {
	Domains []string `json:"Domains"`
}

func deployLucky(cert *deploy.NativeCertificate, cfg map[string]string) error {
	base, err := util.BaseURL(cfg["url"])
	if err != nil {
		return err
	}
	token := strings.TrimSpace(cfg["open_token"])
	if token == "" {
		return i18n.E("service.deploy.lucky_token_required")
	}
	c := &client{
		base: base, safePath: strings.Trim(strings.TrimSpace(cfg["safe_path"]), "/"),
		openToken: token, http: util.Client(cfg),
	}

	list, err := c.listCertificates()
	if err != nil {
		return err
	}
	keys := util.SplitList(cfg["cert_keys"])
	if len(keys) == 0 {
		domains := util.Domains(cert)
		for _, item := range list {
			if util.MatchAny(domains, domainsOf(item)) {
				keys = append(keys, item.Key)
			}
		}
	}
	if len(keys) == 0 {
		return i18n.E("service.deploy.lucky_cert_not_found")
	}

	byKey := make(map[string]certificate, len(list))
	for _, item := range list {
		byKey[item.Key] = item
	}
	var errs []string
	for _, key := range keys {
		item, ok := byKey[key]
		if !ok {
			errs = append(errs, key+": "+i18n.E("service.deploy.lucky_cert_key_not_found", "key", key).Error())
			continue
		}
		if err := c.updateCertificate(cert, key, item.Remark); err != nil {
			errs = append(errs, key+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return i18n.E("service.deploy.partial_failed", "errors", strings.Join(errs, "; "))
	}
	return nil
}

func (c *client) endpoint(path string) string {
	if c.safePath != "" {
		return util.JoinURL(util.JoinURL(c.base, c.safePath), path)
	}
	return util.JoinURL(c.base, path)
}

func (c *client) do(method, path string, payload any, out any) error {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return i18n.E("service.deploy.request_build_failed", "error", err.Error())
		}
	}
	endpoint := c.endpoint(path)
	separator := "?"
	if strings.Contains(endpoint, "?") {
		separator = "&"
	}
	endpoint += separator + "_=" + strconv.FormatInt(time.Now().UnixMilli(), 10)
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	if err != nil {
		return i18n.E("service.deploy.request_build_failed", "error", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("openToken", c.openToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return i18n.E("service.deploy.lucky_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result response
	if err := json.Unmarshal(raw, &result); err != nil {
		return i18n.E("service.deploy.lucky_response_invalid", "error", err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Ret == nil || *result.Ret != 0 {
		message := result.Msg
		if message == "" {
			message = util.Truncate(string(raw), 300)
		}
		return i18n.E("service.deploy.lucky_error", "status", resp.StatusCode, "message", message)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return i18n.E("service.deploy.lucky_response_invalid", "error", err.Error())
		}
	}
	return nil
}

func (c *client) listCertificates() ([]certificate, error) {
	var result response
	if err := c.do(http.MethodGet, "/api/ssl", nil, &result); err != nil {
		return nil, err
	}
	return result.List, nil
}

func (c *client) updateCertificate(cert *deploy.NativeCertificate, key, remark string) error {
	return c.do(http.MethodPut, "/api/ssl", map[string]any{
		"AddFrom":             "file",
		"CertBase64":          base64.StdEncoding.EncodeToString([]byte(cert.FullchainPEM)),
		"Enable":              true,
		"Key":                 key,
		"MappingChangeScript": "",
		"MappingPath":         "",
		"MappingToPath":       false,
		"KeyBase64":           base64.StdEncoding.EncodeToString([]byte(cert.KeyPEM)),
		"IssuerCertificate":   "",
		"ExtParams":           map[string]any{},
		"Remark":              remark,
	}, nil)
}

func domainsOf(item certificate) []string {
	if len(item.CertsInfo) == 0 || string(item.CertsInfo) == "null" {
		return nil
	}
	// CertsInfo 通常是对象，个别版本是数组。
	var one certsInfo
	if json.Unmarshal(item.CertsInfo, &one) == nil && len(one.Domains) > 0 {
		return one.Domains
	}
	var list []certsInfo
	if json.Unmarshal(item.CertsInfo, &list) == nil {
		var out []string
		for _, info := range list {
			out = append(out, info.Domains...)
		}
		return out
	}
	return nil
}
