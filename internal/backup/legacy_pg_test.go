package backup

import (
	"os"
	"testing"
)

func TestLegacyPostgresRestoreIntegration(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_PG_LEGACY_RESTORE_DSN")
	file := os.Getenv("LACTSSL_TEST_PG_LEGACY_FILE")
	if dsn == "" || file == "" {
		t.Skip("legacy PG env not set")
	}
	if err := ApplyPostgresDatabase(dsn, file); err != nil {
		t.Fatal(err)
	}
}
