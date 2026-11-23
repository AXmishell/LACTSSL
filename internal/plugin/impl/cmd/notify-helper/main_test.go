package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/AXmishell/LACTSSL-Plugins/protocol"
	"lactssl/internal/plugin/impl/notify/providers/channels"
)

func TestMain(m *testing.M) {
	channels.RegisterAll()
	os.Exit(m.Run())
}

func TestManifestMatchesLegacy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "legacy-notify-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want protocol.NotifyManifest
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}

	got := buildManifest()
	got.Version = ""
	got.Name = ""
	want.Version = ""
	want.Name = ""

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest differs: got %d channels, want %d", len(got.Channels), len(want.Channels))
	}
	if len(got.Channels) != 23 {
		t.Fatalf("channels = %d, want 23", len(got.Channels))
	}
}
