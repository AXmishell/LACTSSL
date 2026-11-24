package channels

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	notify "lactssl/providers/api/notify"
)

func init() {
	registerChannel(newChannel("serverchan", []notify.NativeField{
		password("sendkey", "notifications.fieldSendKey", true),
		selectField("version", "notifications.fieldVersion", "auto",
			opt("auto", "notifications.serverchanVersionAuto"),
			opt("turbo", "notifications.serverchanVersionTurbo"),
			opt("sc3", "notifications.serverchanVersionSC3"),
		),
	}, sendServerChan))
}

func sendServerChan(cfg map[string]any, msg notify.NativeMessage) error {
	sendKey := cfgString(cfg, "sendkey", "")
	if sendKey == "" {
		return i18n.E("service.notify.serverchan_not_configured")
	}
	endpoint, err := serverChanEndpoint(sendKey, cfgString(cfg, "version", "auto"))
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("title", msg.Title)
	form.Set("desp", msg.Body)
	resp, err := outbound.NewClient(15*time.Second, false).PostForm(endpoint, form)
	if err != nil {
		return i18n.E("service.notify.serverchan_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	body, _ := readLimited(resp.Body, 2048)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text := strings.TrimSpace(string(body))
		if text == "" {
			text = resp.Status
		}
		return i18n.E("service.notify.serverchan_failed", "error", text)
	}
	var result struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return i18n.E("service.notify.serverchan_failed", "error", "invalid JSON response: "+truncateText(string(body), 200))
	}
	if result.Code == nil || *result.Code != 0 {
		text := strings.TrimSpace(result.Message)
		if text == "" && result.Code != nil {
			text = strconv.Itoa(*result.Code)
		}
		if text == "" {
			text = truncateText(string(body), 200)
		}
		return i18n.E("service.notify.serverchan_failed", "error", text)
	}
	return nil
}
