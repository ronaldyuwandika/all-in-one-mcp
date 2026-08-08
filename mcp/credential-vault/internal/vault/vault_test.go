package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	vaultcrypto "github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault/internal/crypto"
)

type testKeys struct {
	key      []byte
	getCalls int
	setCalls int
}

func (k *testKeys) Get() ([]byte, error) {
	k.getCalls++
	if k.key == nil {
		return nil, keyring.ErrNotFound
	}
	return k.key, nil
}
func (k *testKeys) Set(v []byte) error {
	k.setCalls++
	k.key = bytes.Clone(v)
	return nil
}
func testVault(t *testing.T) *Vault {
	t.Helper()
	return New(t.TempDir(), vaultcrypto.New(&testKeys{}))
}
func TestSetGetAudit(t *testing.T) {
	t.Parallel()
	v := testVault(t)
	if err := v.Set("chat.token", "top-secret", "test"); err != nil {
		t.Fatal(err)
	}
	got, err := v.Get("chat.token", "unit test")
	if err != nil {
		t.Fatal(err)
	}
	if got != "top-secret" {
		t.Fatalf("got %q", got)
	}
	a, err := v.Audit(0)
	if err != nil || len(a) != 2 {
		t.Fatalf("audit=%v err=%v", a, err)
	}
	raw, _ := os.ReadFile(v.vaultPath())
	if strings.Contains(string(raw), "top-secret") {
		t.Fatal("vault file contains plaintext")
	}
}
func TestScanRedactRestore(t *testing.T) {
	t.Parallel()
	v := testVault(t)
	root := t.TempDir()
	p := filepath.Join(root, ".env")
	secret := "ghp_" + strings.Repeat("x", 24)
	original := "GITHUB_TOKEN=" + secret
	if err := os.WriteFile(p, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := v.ScanDir(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("no credentials detected")
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), secret) {
		t.Fatal("secret remains after redaction")
	}
	n, err := v.Restore()
	if err != nil || n != 1 {
		t.Fatalf("restored=%d err=%v", n, err)
	}
	raw, _ = os.ReadFile(p)
	if string(raw) != original {
		t.Fatalf("restore mismatch: %q", raw)
	}
}

func TestDetectDoesNotTouchVaultOrAudit(t *testing.T) {
	t.Parallel()
	keys := &testKeys{}
	v := New(t.TempDir(), vaultcrypto.New(keys))
	root := t.TempDir()
	secret := "ghp_" + strings.Repeat("z", 24)
	p := filepath.Join(root, ".env")
	if err := os.WriteFile(p, []byte("TOKEN=[REDACTED], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.DetectDir(root); err != nil {
		t.Fatal(err)
	}
	if keys.getCalls != 0 || keys.setCalls != 0 {
		t.Fatalf("detect used keychain: gets=%d sets=%d", keys.getCalls, keys.setCalls)
	}
	if _, err := os.Stat(v.vaultPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("vault was touched: %v", err)
	}
	if _, err := os.Stat(v.AuditPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("audit was touched: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "TOKEN=[REDACTED] {
		t.Fatalf("target changed: %q, %v", got, err)
	}
}

func TestRedactDirSkipsVaultInternals(t *testing.T) {
	t.Parallel()
	vaultDir := t.TempDir()
	v := New(vaultDir, vaultcrypto.New(&testKeys{}))
	root := filepath.Dir(vaultDir)
	secret := "ghp_" + strings.Repeat("q", 24)
	p := filepath.Join(root, "target.env")
	if err := os.WriteFile(p, []byte("TOKEN=[REDACTED], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("chat.keep", "value", "test"); err != nil {
		t.Fatal(err)
	}
	if err := v.AppendAudit(AuditEntry{Action: "test", Purpose: secret}); err != nil {
		t.Fatal(err)
	}
	tmpPath := filepath.Join(vaultDir, "vault.json.tmp")
	tmpContent := []byte("TOKEN=" + secret)
	if err := os.WriteFile(tmpPath, tmpContent, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(v.vaultPath())

	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.RedactDir(root); err != nil {
		t.Fatal(err)
	}
	data, err := v.Load()
	if err != nil {
		t.Fatalf("vault became undecryptable: %v", err)
	}
	for backupPath := range data.Files {
		if isVaultInternalPath(backupPath, vaultDir) {
			t.Fatalf("vault internal file was backed up: %s", backupPath)
		}
	}
	after, err := os.ReadFile(v.vaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) {
		t.Fatal("vault was not updated with redaction backup")
	}
	if restored, err := v.Get("chat.keep", "test"); err != nil || restored != "value" {
		t.Fatalf("stored credential unavailable: %q, %v", restored, err)
	}
}

func TestRedactFileRejectsOversizeAndSymlink(t *testing.T) {
	t.Parallel()
	v := testVault(t)
	root := t.TempDir()
	large := filepath.Join(root, "large.env")
	original := bytes.Repeat([]byte("x"), maxScanFileBytes+1)
	if err := os.WriteFile(large, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.RedactFile(large); err == nil {
		t.Fatal("expected oversized file rejection")
	}
	got, err := os.ReadFile(large)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("oversized file changed")
	}
	regular := filepath.Join(root, "regular.env")
	if err := os.WriteFile(regular, []byte("TOKEN=[REDACTED]+strings.Repeat("x", 24)), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.env")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	if err := v.RedactFile(link); err == nil {
		t.Fatal("expected symlink rejection")
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := v.RedactFile(directory); err == nil {
		t.Fatal("expected non-regular file rejection")
	}
	got, err = os.ReadFile(regular)
	if err != nil || !strings.Contains(string(got), "ghp_") {
		t.Fatal("symlink target changed")
	}
}

func TestRestoreValidatesAllTargetsBeforeWriting(t *testing.T) {
	t.Parallel()
	v := testVault(t)
	root := t.TempDir()
	first := filepath.Join(root, "a.env")
	second := filepath.Join(root, "b.env")
	if err := os.WriteFile(first, []byte("masked-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("masked-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	data := Data{Credentials: map[string]Credential{}, Files: map[string]FileBackup{
		first:  {Content: "original-a", Mode: 0o600},
		second: {Content: "original-b", Mode: 0o600},
		link:   {Content: "should-not-write", Mode: 0o600},
	}, ScanRoots: []string{root}}
	if err := v.saveDataForTest(data); err != nil {
		t.Fatal(err)
	}
	n, err := v.Restore()
	if err == nil || n != 0 {
		t.Fatalf("restore progress=%d err=%v, want zero writes", n, err)
	}
	for _, path := range []string{first, second} {
		got, readErr := os.ReadFile(path)
		if readErr != nil || !strings.HasPrefix(string(got), "masked") {
			t.Fatalf("target %s changed: %q, %v", path, got, readErr)
		}
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "outside" {
		t.Fatalf("outside target changed: %q, %v", got, err)
	}
}

func TestRestoreReportsPartialProgressAndRetainsBackups(t *testing.T) {
	t.Parallel()
	v := testVault(t)
	root := t.TempDir()
	first := filepath.Join(root, "a.env")
	blockedDir := filepath.Join(root, "blocked")
	second := filepath.Join(blockedDir, "b.env")
	if err := os.Mkdir(blockedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("masked-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("masked-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.saveDataForTest(Data{Credentials: map[string]Credential{}, Files: map[string]FileBackup{
		first:  {Content: "original-a", Mode: 0o600},
		second: {Content: "original-b", Mode: 0o600},
	}, ScanRoots: []string{root}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blockedDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blockedDir, 0o700) })
	n, err := v.Restore()
	if err == nil || n != 1 || !strings.Contains(err.Error(), "restored 1 files") {
		t.Fatalf("restore progress=%d err=%v, want one restored file", n, err)
	}
	got, readErr := os.ReadFile(first)
	if readErr != nil || string(got) != "original-a" {
		t.Fatalf("first target not restored: %q, %v", got, readErr)
	}
	got, readErr = os.ReadFile(second)
	if readErr != nil || string(got) != "masked-b" {
		t.Fatalf("second target changed after failed restore: %q, %v", got, readErr)
	}
	if err := os.Chmod(blockedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if n, err = v.Restore(); err != nil || n != 2 {
		t.Fatalf("retry restore=%d err=%v", n, err)
	}
	got, readErr = os.ReadFile(second)
	if readErr != nil || string(got) != "original-b" {
		t.Fatalf("retry did not restore second target: %q, %v", got, readErr)
	}
}

func TestRestoreRejectsUnapprovedLegacyPath(t *testing.T) {
	t.Parallel()
	v := testVault(t)
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.saveDataForTest(Data{Credentials: map[string]Credential{}, Files: map[string]FileBackup{outside: {Content: "changed", Mode: 0o600}}, ScanRoots: []string{root}}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Restore(); err == nil {
		t.Fatal("expected restore rejection")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "original" {
		t.Fatalf("outside target changed: %q, %v", got, err)
	}

	linkRoot := filepath.Join(root, "link")
	outsideDir := t.TempDir()
	outsideLinked := filepath.Join(outsideDir, "linked")
	if err := os.WriteFile(outsideLinked, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, linkRoot); err != nil {
		t.Fatal(err)
	}
	if err := v.saveDataForTest(Data{Credentials: map[string]Credential{}, Files: map[string]FileBackup{filepath.Join(linkRoot, "linked"): {Content: "changed", Mode: 0o600}}, ScanRoots: []string{root}}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Restore(); err == nil {
		t.Fatal("expected symlink restore rejection")
	}
	got, err = os.ReadFile(outsideLinked)
	if err != nil || string(got) != "original" {
		t.Fatalf("symlink target changed: %q, %v", got, err)
	}
}

func (v *Vault) saveDataForTest(data Data) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.saveUnlocked(data)
}
func TestStatsReportsOversizedVaultWithoutZeroingFileSize(t *testing.T) {
	t.Parallel()
	v := testVault(t)
	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(v.vaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(maxVaultBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := v.Stats()
	if err == nil || !strings.Contains(err.Error(), "vault file exceeds") {
		t.Fatalf("stats err=%v, want oversized vault error", err)
	}
	if s.VaultFileSizeBytes != maxVaultBytes+1 {
		t.Fatalf("vault size=%d, want %d", s.VaultFileSizeBytes, maxVaultBytes+1)
	}
}

func TestImportBackupCredentialsDeduplicatesAndPreservesTarget(t *testing.T) {
	t.Parallel()
	vTarget := testVault(t)
	if err := vTarget.Set("chat.existing", "keep-me", "test"); err != nil {
		t.Fatal(err)
	}

	backupData := Data{
		Credentials: map[string]Credential{
			"chat.existing": {Value: "overwrite-me", Source: "backup", CreatedAt: time.Now().UTC()},
			"chat.new":      {Value: "new-secret", Source: "backup", CreatedAt: time.Now().UTC()},
			"chat.other":    {Value: "ignore-me", Source: "backup", CreatedAt: time.Now().UTC()},
		},
		Files: map[string]FileBackup{
			"/tmp/ignored": {Content: "ignored", Mode: 0o600},
		},
	}
	raw, err := json.Marshal(backupData)
	if err != nil {
		t.Fatal(err)
	}
	token, err := vTarget.crypt.Encrypt(raw)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "vault.json.bak")
	if err := os.WriteFile(backupPath, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}

	names, err := vTarget.ListCredentialNames(backupPath, 2)
	if err != nil || len(names) != 2 {
		t.Fatalf("ListCredentialNames=%v err=%v, want 2 names", names, err)
	}

	imported, skipped, err := vTarget.ImportBackupCredentials(backupPath, []string{"chat.existing", "chat.new"})
	if err != nil {
		t.Fatalf("ImportBackupCredentials: %v", err)
	}
	if imported != 1 || skipped != 1 {
		t.Fatalf("imported=%d skipped=%d, want 1, 1", imported, skipped)
	}

	gotExisting, err := vTarget.Get("chat.existing", "test")
	if err != nil || gotExisting != "keep-me" {
		t.Fatalf("existing value changed: %q, %v", gotExisting, err)
	}
	gotNew, err := vTarget.Get("chat.new", "test")
	if err != nil || gotNew != "new-secret" {
		t.Fatalf("new value missing/wrong: %q, %v", gotNew, err)
	}
	if _, err = vTarget.Get("chat.other", "test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unrequested credential imported: %v", err)
	}

	data, err := vTarget.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Files) != 0 {
		t.Fatalf("files imported unexpectedly: %+v", data.Files)
	}
}

func TestMaskText(t *testing.T) {
	t.Parallel()
	in := "token " + "ghp_" + strings.Repeat("y", 24) + " and PASSWORD=" + strings.Repeat("p", 16)
	out := MaskText(in)
	if strings.Contains(out, "ghp_") || strings.Contains(out, strings.Repeat("p", 16)) {
		t.Fatalf("unmasked: %s", out)
	}
}
