package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	s := Stats{}
	if info, err := os.Stat(v.vaultPath()); err == nil {
		s.VaultFileSizeBytes = info.Size()
	}
	d, err := v.Load()
	if err != nil {
		return s, err
	}
	now := time.Now()
	s.TotalCredentials = len(d.Credentials)
	s.RedactedFilesCount = len(d.Files)
	s.LastScanTS = d.LastScan
	s.KeychainAccessible = v.crypt.Probe() == nil
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

// ImportBackupCredentials selectively imports named credentials from a raw encrypted vault file.
func (v *Vault) ImportBackupCredentials(path string, names []string) (int, int, error) {
	selected, skipped, err := v.readSelectedCredentials(path, names)
	if err != nil {
		return 0, 0, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	targetData, err := v.loadUnlocked()
	if err != nil {
		return 0, 0, fmt.Errorf("load target vault: %w", err)
	}
	imported := 0
	for name, credential := range selected {
		if _, exists := targetData.Credentials[name]; exists {
			skipped++
			continue
		}
		targetData.Credentials[name] = credential
		imported++
	}
	if imported == 0 {
		return 0, skipped, nil
	}
	if err = v.saveUnlocked(targetData); err != nil {
		return 0, skipped, fmt.Errorf("save target vault: %w", err)
	}
	if err = v.auditUnlocked(AuditEntry{Action: "import-backup", Purpose: fmt.Sprintf("imported %d credentials", imported)}); err != nil {
		return imported, skipped, fmt.Errorf("audit backup import: %w", err)
	}
	return imported, skipped, nil
}

func (v *Vault) ListCredentialNames(path string, limit int) ([]string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open vault file: %w", err)
	}
	defer f.Close()
	reader, err := v.crypt.NewDecryptReader(f)
	if err != nil {
		return nil, fmt.Errorf("decrypt vault: %w", err)
	}
	decoder := json.NewDecoder(reader)
	names := make([]string, 0)
	tok, err := decoder.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, errors.New("invalid JSON payload in vault")
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("read vault field: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("vault field name is not a string")
		}
		if key != "credentials" {
			if err = skipJSONValue(decoder); err != nil {
				return nil, fmt.Errorf("skip vault field %s: %w", key, err)
			}
			continue
		}
		valueToken, err := decoder.Token()
		if err != nil || valueToken != json.Delim('{') {
			return nil, errors.New("credentials field is not an object")
		}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return nil, fmt.Errorf("read credential name: %w", err)
			}
			name, ok := nameToken.(string)
			if !ok {
				return nil, errors.New("credential name is not a string")
			}
			if limit <= 0 || len(names) < limit {
				names = append(names, name)
			}
			if err = skipJSONValue(decoder); err != nil {
				return nil, fmt.Errorf("skip credential %s: %w", name, err)
			}
		}
		if _, err = decoder.Token(); err != nil {
			return nil, fmt.Errorf("close credentials field: %w", err)
		}
	}
	if _, err = decoder.Token(); err != nil {
		return nil, fmt.Errorf("close vault payload: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("vault contains trailing JSON data")
		}
		return nil, fmt.Errorf("verify vault: %w", err)
	}
	return names, nil
}

func (v *Vault) RecoverCredentials(path string, names []string) (int, error) {
	selected, _, err := v.readSelectedCredentials(path, names)
	if err != nil {
		return 0, err
	}
	data := emptyData()
	for name, credential := range selected {
		data.Credentials[name] = credential
	}
	if len(data.Credentials) == 0 {
		return 0, errors.New("no requested credentials found")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err = v.saveUnlocked(data); err != nil {
		return 0, fmt.Errorf("save recovered vault: %w", err)
	}
	return len(data.Credentials), nil
}

func (v *Vault) readSelectedCredentials(path string, names []string) (map[string]Credential, int, error) {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name != "" {
			wanted[name] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil, 0, errors.New("at least one credential name is required")
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, 0, fmt.Errorf("open vault file: %w", err)
	}
	defer f.Close()
	reader, err := v.crypt.NewDecryptReader(f)
	if err != nil {
		return nil, 0, fmt.Errorf("decrypt vault: %w", err)
	}
	decoder := json.NewDecoder(reader)
	selected := make(map[string]Credential, len(wanted))
	skipped := 0
	tok, err := decoder.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, 0, errors.New("invalid JSON payload in vault")
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, 0, fmt.Errorf("read vault field: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, 0, errors.New("vault field name is not a string")
		}
		if key != "credentials" {
			if err = skipJSONValue(decoder); err != nil {
				return nil, 0, fmt.Errorf("skip vault field %s: %w", key, err)
			}
			continue
		}
		valueToken, err := decoder.Token()
		if err != nil || valueToken != json.Delim('{') {
			return nil, 0, errors.New("credentials field is not an object")
		}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return nil, 0, fmt.Errorf("read credential name: %w", err)
			}
			name, ok := nameToken.(string)
			if !ok {
				return nil, 0, errors.New("credential name is not a string")
			}
			if _, ok = wanted[name]; !ok {
				if err = skipJSONValue(decoder); err != nil {
					return nil, 0, fmt.Errorf("skip credential %s: %w", name, err)
				}
				continue
			}
			if _, ok = selected[name]; ok {
				skipped++
				if err = skipJSONValue(decoder); err != nil {
					return nil, 0, fmt.Errorf("skip duplicate credential: %w", err)
				}
				continue
			}
			var credential Credential
			if err = decoder.Decode(&credential); err != nil {
				return nil, 0, fmt.Errorf("decode credential %s: %w", name, err)
			}
			selected[name] = credential
		}
		if _, err = decoder.Token(); err != nil {
			return nil, 0, fmt.Errorf("close credentials field: %w", err)
		}
	}
	if _, err = decoder.Token(); err != nil {
		return nil, 0, fmt.Errorf("close vault payload: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, 0, errors.New("vault contains trailing JSON data")
		}
		return nil, 0, fmt.Errorf("verify vault: %w", err)
	}
	return selected, skipped, nil
}

func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok || (delim != '{' && delim != '[') {
		return nil
	}
	depth := 1
	for depth > 0 {
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok = token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}
