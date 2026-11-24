package notifyplugin_test

import (
	"reflect"
	"testing"

	notify "lactssl/internal/plugin/host/notify"
	"lactssl/providers/notify/channels"
)

func TestOfficialChannelIDsMatchRegistry(t *testing.T) {
	want := channels.IDs()
	got := notify.OfficialChannelIDs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("host/notify/ids.go 与 channels 注册表不一致:\n got=%v\nwant=%v", got, want)
	}
}
