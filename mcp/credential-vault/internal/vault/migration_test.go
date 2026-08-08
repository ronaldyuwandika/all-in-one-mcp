package vault

import (
	"testing"
)

func TestImportLegacyMergesRecords(t *testing.T) {
	v := testVault(t)
	count, err := v.ImportLegacy(LegacyImport{
		Credentials: map[string]string{"legacy.token": "fixture-value"},
		Files:       map[string]FileBackup{"/tmp/legacy-fixture": {Content: "backup", Mode: 0o600}},
	})
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	d, err := v.Load()
	if err != nil {
		t.Fatal(err)
	}
	if d.Credentials["legacy.token"].Value != "fixture-value" || d.Files["/tmp/legacy-fixture"].Content != "backup" {
		t.Fatal("legacy records were not persisted")
	}
}
