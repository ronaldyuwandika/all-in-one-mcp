package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Stats struct {
	TotalCredentials        int       `json:"total_credentials"`
	FileCredentials         int       `json:"file_credentials"`
	ChatCredentials         int       `json:"chat_credentials"`
	RedactedFilesCount      int       `json:"redacted_files_count"`
	LastScanTS              time.Time `json:"last_scan_ts"`
	VaultAgeDays            int       `json:"vault_age_days"`
	AuditEntriesTotal       int       `json:"audit_entries_total"`
	AuditEntries24H         int       `json:"audit_entries_24h"`
	KeychainAccessible      bool      `json:"keychain_accessible"`
	VaultFileSizeBytes      int64     `json:"vault_file_size_bytes"`
	OldestCredentialAgeDays int       `json:"oldest_credential_age_days"`
	NewestCredentialAgeDays int       `json:"newest_credential_age_days"`
}

func (v *Vault) Stats() (Stats, error) {
	d, err := v.Load()
	if err != nil {
		return Stats{}, err
	}
	now := time.Now()
	s := Stats{TotalCredentials: len(d.Credentials), RedactedFilesCount: len(d.Files), LastScanTS: d.LastScan, KeychainAccessible: v.crypt.Probe() == nil}
	for k, c := range d.Credentials {
		if strings.HasPrefix(k, "chat.") {
			s.ChatCredentials++
		} else {
			s.FileCredentials++
		}
		age := int(now.Sub(c.CreatedAt).Hours() / 24)
		if age > s.OldestCredentialAgeDays {
			s.OldestCredentialAgeDays = age
		}
		if s.NewestCredentialAgeDays == 0 || age < s.NewestCredentialAgeDays {
			s.NewestCredentialAgeDays = age
		}
	}
	if !d.CreatedAt.IsZero() {
		s.VaultAgeDays = int(now.Sub(d.CreatedAt).Hours() / 24)
	}
	a, err := v.Audit(0)
	if err != nil {
		return Stats{}, err
	}
	s.AuditEntriesTotal = len(a)
	for _, e := range a {
		if now.Sub(e.Timestamp) <= 24*time.Hour {
			s.AuditEntries24H++
		}
	}
	if info, e := os.Stat(v.vaultPath()); e == nil {
		s.VaultFileSizeBytes = info.Size()
	}
	return s, nil
}

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}
type DoctorReport struct {
	Status   string  `json:"status"`
	ExitCode int     `json:"exit_code"`
	Checks   []Check `json:"checks"`
}

func (v *Vault) Doctor() DoctorReport {
	r := DoctorReport{Status: "healthy"}
	add := func(n, s, m string) {
		r.Checks = append(r.Checks, Check{n, s, m})
		if s == "critical" {
			r.Status = "critical"
			r.ExitCode = 2
		} else if s == "warning" && r.ExitCode < 1 {
			r.Status = "warning"
			r.ExitCode = 1
		}
	}
	d, err := v.Load()
	if err != nil {
		add("vault_decryptable", "critical", err.Error())
		return r
	}
	add("vault_decryptable", "ok", "vault is decryptable")
	if err = v.crypt.Probe(); err != nil {
		add("keychain_accessible", "critical", err.Error())
	} else {
		add("keychain_accessible", "ok", "OS keyring is accessible")
	}
	if info, e := os.Stat(v.dir); e == nil && info.Mode().Perm() != 0o700 {
		add("directory_permissions", "warning", fmt.Sprintf("mode is %o, expected 700", info.Mode().Perm()))
	} else {
		add("directory_permissions", "ok", "mode is 0700")
	}
	if info, e := os.Stat(v.AuditPath()); e == nil && info.Size() > 100<<20 {
		add("audit_size", "warning", "audit log exceeds 100MB")
	} else {
		add("audit_size", "ok", "audit log is readable and bounded")
	}
	for p := range d.Files {
		raw, e := os.ReadFile(p) // #nosec G304 -- paths originate from the authenticated encrypted vault backup map.
		if e != nil {
			add("redacted_files", "critical", p+": "+e.Error())
			continue
		}
		if !strings.Contains(string(raw), "[REDACTED]") {
			add("redacted_files", "warning", p+" has a backup but no redaction marker")
		}
	}
	for n, c := range d.Credentials {
		if time.Since(c.CreatedAt) > 365*24*time.Hour {
			add("credential_age", "warning", n+" is older than 365 days")
		}
	}
	if len(r.Checks) == 4 {
		add("credentials", "ok", "credential ages are within policy")
	}
	return r
}

type Export struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Encrypted string    `json:"encrypted"`
}

// LegacyImport is the value-only stream accepted from the Python vault migrator.
type LegacyImport struct {
	Credentials map[string]string     `json:"credentials"`
	Files       map[string]FileBackup `json:"files"`
	Audit       []AuditEntry          `json:"audit"`
}

// ImportLegacy merges decrypted legacy records into the encrypted Go vault.
func (v *Vault) ImportLegacy(in LegacyImport) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	d, err := v.loadUnlocked()
	if err != nil {
		return 0, err
	}
	count := 0
	for name, value := range in.Credentials {
		if _, exists := d.Credentials[name]; !exists {
			d.Credentials[name] = Credential{Value: value, Source: "legacy-python", CreatedAt: time.Now().UTC()}
			count++
		}
	}
	for path, backup := range in.Files {
		if _, exists := d.Files[path]; !exists {
			d.Files[path] = backup
		}
	}
	if err = v.saveUnlocked(d); err != nil {
		return 0, err
	}
	for _, entry := range in.Audit {
		if err = v.auditUnlocked(entry); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func (v *Vault) Export(path string) error {
	d, err := v.Load()
	if err != nil {
		return err
	}
	plain, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode export: %w", err)
	}
	token, err := v.crypt.Encrypt(plain)
	if err != nil {
		return fmt.Errorf("encrypt export: %w", err)
	}
	raw, err := json.MarshalIndent(Export{"credential-vault-export", 1, time.Now().UTC(), token}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Clean(path), raw, 0o600)
}
func (v *Vault) Import(path string) error {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}
	var e Export
	if err = json.Unmarshal(raw, &e); err != nil {
		return err
	}
	if e.Format != "credential-vault-export" {
		return fmt.Errorf("invalid export format")
	}
	plain, err := v.crypt.Decrypt(e.Encrypted)
	if err != nil {
		return fmt.Errorf("decrypt export: %w", err)
	}
	var data Data
	if err = json.Unmarshal(plain, &data); err != nil {
		return fmt.Errorf("decode export: %w", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.saveUnlocked(data)
}
