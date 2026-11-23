package notify_test

import (
	"reflect"
	"testing"

	"lactssl/internal/plugin/impl/notify"
	"lactssl/internal/plugin/impl/notify/providers/channels"
)

func TestOfficialChannelIDsMatchRegistry(t *testing.T) {
	want := channels.IDs()
	got := notify.OfficialChannelIDs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notify/ids.go 与 channels 注册表不一致:\n got=%v\nwant=%v", got, want)
	}
}
