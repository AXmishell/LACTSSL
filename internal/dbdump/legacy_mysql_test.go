package dbdump

import (
	"context"
	"os"
	"testing"
)

func TestLegacyMySQLRestoreIntegration(t *testing.T) {
	dsn := os.Getenv("LACTSSL_TEST_MYSQL_LEGACY_DSN")
	file := os.Getenv("LACTSSL_TEST_MYSQL_LEGACY_FILE")
	if dsn == "" || file == "" {
		t.Skip("legacy MySQL env not set")
	}
	if err := RestoreMySQL(context.Background(), dsn, file); err != nil {
		t.Fatal(err)
	}
}
