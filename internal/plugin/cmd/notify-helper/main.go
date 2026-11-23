// Command notify-helper 是 LACTSSL 通知插件协议的官方实现。
//
// 主程序通过一次性子进程调用它：
//
//	notify-helper manifest   # 输出插件清单 JSON
//	notify-helper send       # 从 stdin 读取 JSON 请求并发送通知
package main

import (
	"os"
	"runtime/debug"

	"github.com/AXmishell/LACTSSL-Plugins/i18n"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"github.com/AXmishell/LACTSSL-Plugins/sdk/helper"
	notify "lactssl/internal/plugin/notify"
	"lactssl/internal/plugin/notify/providers/channels"
)

func main() {
	defer helper.Recover()
	channels.RegisterAll()
	helper.ApplyMemoryLimit()
	if len(os.Args) < 2 {
		helper.Fail("用法: notify-helper manifest|send")
	}
	switch os.Args[1] {
	case "manifest", "list":
		writeManifest()
	case "send":
		handle()
	default:
		helper.Fail("未知子命令: " + os.Args[1])
	}
}

func writeManifest() {
	if err := helper.WriteJSON(buildManifest()); err != nil {
		helper.Fail("输出 manifest 失败: " + err.Error())
	}
}

func buildManifest() protocol.NotifyManifest {
	plugins := notify.ListNative()
	manifest := protocol.NotifyManifest{
		Protocol: protocol.ProtocolVersion,
		Name:     "lactssl notify helper",
		Version:  serviceVersion(),
		Channels: make([]protocol.NotifyChannel, 0, len(plugins)),
	}
	for _, plugin := range plugins {
		def := plugin.Definition()
		manifest.Channels = append(manifest.Channels, protocol.NotifyChannel{
			ID:          def.ID,
			Name:        def.Name,
			Description: def.Description,
			Aliases:     def.Aliases,
			Fields:      def.Fields,
		})
	}
	return manifest
}

func handle() {
	var req protocol.NotifyRequest
	if err := helper.ReadJSON(&req); err != nil {
		helper.Fail("解析请求失败: " + err.Error())
	}
	if req.Protocol != "" && req.Protocol != protocol.ProtocolVersion {
		helper.Fail("不支持的插件协议: " + req.Protocol)
	}
	if req.Action != "" && req.Action != "send" {
		helper.Fail("不支持的通知动作: " + req.Action)
	}
	if req.Channel == "" {
		helper.Fail("缺少通知渠道")
	}
	channel, ok := notify.LookupNative(req.Channel)
	if !ok {
		helper.Fail("未知通知渠道: " + req.Channel)
	}
	if err := channel.Notify(req.Config, notify.NativeMessage{Title: req.Title, Body: req.Body}); err != nil {
		fail(err.Error())
	}
	writeResult(true, "")
}

func writeResult(ok bool, message string) {
	_ = helper.WriteJSON(map[string]any{"ok": ok, "message": message})
}

// buildVersion 由 pluginctl 通过 -ldflags 注入，必须与仓库索引中的版本一致。
var buildVersion = ""

func serviceVersion() string {
	if buildVersion != "" {
		return buildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func fail(message string) {
	writeResult(false, message)
	helper.Fail(i18n.T(i18n.Default, "plugin.notify.failed", map[string]any{"error": message}))
}
