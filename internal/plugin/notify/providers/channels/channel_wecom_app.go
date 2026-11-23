package channels

import (
	"encoding/json"
	"io"
	"net/url"
	"time"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	notify "lactssl/internal/plugin/notify"
)

func init() {
	registerChannel(newChannel("wecom_app", []notify.NativeField{
		text("corp_id", "notifications.fieldCorpId", true),
		password("corp_secret", "notifications.fieldCorpSecret", true),
		number("agent_id", "notifications.fieldAgentId", 0),
		text("to_user", "notifications.fieldToUser", false),
	}, sendWecomApp))
}

func sendWecomApp(cfg map[string]any, msg notify.NativeMessage) error {
	corpID := cfgString(cfg, "corp_id", "")
	corpSecret := cfgString(cfg, "corp_secret", "")
	agentID := cfgInt(cfg, "agent_id", 0)
	if corpID == "" || corpSecret == "" || agentID == 0 {
		return missingChannelConfig("wecom_app")
	}
	tokenURL := "https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=" + url.QueryEscape(corpID) + "&corpsecret=" + url.QueryEscape(corpSecret)
	resp, err := outbound.NewClient(15*time.Second, false).Get(tokenURL)
	if err != nil {
		return i18n.E("service.notify.channel_request_failed", "error", outbound.SafeError(err))
	}
	defer resp.Body.Close()
	var tokenResult struct {
		AccessToken string `json:"access_token"`
		ErrMsg      string `json:"errmsg"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(raw, &tokenResult); err != nil {
		return err
	}
	if tokenResult.AccessToken == "" {
		return i18n.E("service.notify.channel_failed", "error", tokenResult.ErrMsg)
	}
	toUser := cfgString(cfg, "to_user", "@all")
	return notifyJSON("https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token="+url.QueryEscape(tokenResult.AccessToken), map[string]any{
		"touser":  toUser,
		"msgtype": "text",
		"agentid": agentID,
		"text":    map[string]any{"content": truncateUTF8Bytes(channelMessage(msg.Title, msg.Body), 2000)},
	}, nil)
}
