// Package vault provides local encrypted credential storage, scanning, redaction, and auditing.
package vault

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	vaultcrypto "github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/crypto"
	"github.com/ronaldyuwandika/all-in-one-mcp/pkg/secretdetect"
)

var ErrNotFound = errors.New("credential not found")

type Credential struct {
	Value     string    `json:"value"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}
type FileBackup struct {
	Content string      `json:"content"`
	Mode    fs.FileMode `json:"mode"`
}
type Data struct {
	Credentials map[string]Credential `json:"credentials"`
	Files       map[string]FileBackup `json:"files"`
	CreatedAt   time.Time             `json:"created_at"`
	LastScan    time.Time             `json:"last_scan"`
}
type AuditEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Action     string    `json:"action"`
	Credential string    `json:"credential,omitempty"`
	Purpose    string    `json:"purpose,omitempty"`
}

type Vault struct {
	dir   string
	crypt *vaultcrypto.Fernet
	mu    sync.Mutex
}

func New(dir string, crypt *vaultcrypto.Fernet) *Vault { return &Vault{dir: dir, crypt: crypt} }
func Default() (*Vault, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return New(filepath.Join(home, ".credential-vault-go"), vaultcrypto.New(vaultcrypto.SystemKeyStore{})), nil
}
func (v *Vault) vaultPath() string { return filepath.Join(v.dir, "vault.json") }
func (v *Vault) AuditPath() string { return filepath.Join(v.dir, "audit.jsonl") }

func emptyData() Data {
	return Data{Credentials: map[string]Credential{}, Files: map[string]FileBackup{}, CreatedAt: time.Now().UTC()}
}
func (v *Vault) loadUnlocked() (Data, error) {
	raw, err := os.ReadFile(v.vaultPath())
	if errors.Is(err, os.ErrNotExist) {
		return emptyData(), nil
	}
	if err != nil {
		return Data{}, fmt.Errorf("read vault: %w", err)
	}
	plain, err := v.crypt.Decrypt(strings.TrimSpace(string(raw)))
	if err != nil {
		return Data{}, fmt.Errorf("decrypt vault: %w", err)
	}
	var data Data
	if err = json.Unmarshal(plain, &data); err != nil {
		return Data{}, fmt.Errorf("decode vault: %w", err)
	}
	if data.Credentials == nil {
		data.Credentials = map[string]Credential{}
	}
	if data.Files == nil {
		data.Files = map[string]FileBackup{}
	}
	return data, nil
}
func (v *Vault) Load() (Data, error) { v.mu.Lock(); defer v.mu.Unlock(); return v.loadUnlocked() }
func (v *Vault) saveUnlocked(data Data) error {
	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		return fmt.Errorf("create vault directory: %w", err)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode vault: %w", err)
	}
	token, err := v.crypt.Encrypt(raw)
	if err != nil {
		return fmt.Errorf("encrypt vault: %w", err)
	}
	tmp := v.vaultPath() + ".tmp"
	if err = os.WriteFile(tmp, []byte(token), 0o600); err != nil {
		return fmt.Errorf("write vault: %w", err)
	}
	if err = os.Rename(tmp, v.vaultPath()); err != nil {
		return fmt.Errorf("replace vault: %w", err)
	}
	return nil
}
func (v *Vault) Set(name, value, source string) error {
	if name == "" || value == "" {
		return errors.New("name and value are required")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	d, err := v.loadUnlocked()
	if err != nil {
		return err
	}
	d.Credentials[name] = Credential{Value: value, Source: source, CreatedAt: time.Now().UTC()}
	if err = v.saveUnlocked(d); err != nil {
		return err
	}
	return v.auditUnlocked(AuditEntry{Action: "set", Credential: name, Purpose: source})
}
func (v *Vault) Get(name, purpose string) (string, error) {
	if purpose == "" {
		return "", errors.New("purpose is required")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	d, err := v.loadUnlocked()
	if err != nil {
		return "", err
	}
	c, ok := d.Credentials[name]
	if !ok {
		return "", ErrNotFound
	}
	if err = v.auditUnlocked(AuditEntry{Action: "get", Credential: name, Purpose: purpose}); err != nil {
		return "", err
	}
	return c.Value, nil
}
func (v *Vault) ClearChat() (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	d, err := v.loadUnlocked()
	if err != nil {
		return 0, err
	}
	n := 0
	for k := range d.Credentials {
		if strings.HasPrefix(k, "chat.") {
			delete(d.Credentials, k)
			n++
		}
	}
	return n, v.saveUnlocked(d)
}
func (v *Vault) auditUnlocked(e AuditEntry) error {
	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(v.AuditPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()
	e.Timestamp = time.Now().UTC()
	return json.NewEncoder(f).Encode(e)
}

// AppendAudit records an event without reading credential values.
func (v *Vault) AppendAudit(e AuditEntry) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.auditUnlocked(e)
}
func (v *Vault) Audit(limit int) ([]AuditEntry, error) {
	f, err := os.Open(v.AuditPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []AuditEntry
	s := bufio.NewScanner(f)
	for s.Scan() {
		var e AuditEntry
		if json.Unmarshal(s.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	if err = s.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func MaskText(text string) string {
	return secretdetect.Redact(text).Text
}

func Detect(text string) map[string]string {
	return secretdetect.DetectValues(text)
}
func (v *Vault) ScanDir(root string, redact bool) (map[string]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	d, err := v.loadUnlocked()
	if err != nil {
		return nil, err
	}
	found := map[string]string{}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve scan root: %w", err)
	}
	safeRoot, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, fmt.Errorf("open scan root: %w", err)
	}
	defer safeRoot.Close()
	err = filepath.WalkDir(absRoot, func(path string, de fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if de.IsDir() {
			if de.Name() == ".git" || de.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if de.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, e := filepath.Rel(absRoot, path)
		if e != nil {
			return nil
		}
		info, e := safeRoot.Stat(rel)
		if e != nil || info.Size() > 2<<20 {
			return nil
		}
		file, e := safeRoot.Open(rel)
		if e != nil {
			return nil
		}
		raw, e := io.ReadAll(io.LimitReader(file, (2<<20)+1))
		_ = file.Close()
		if e != nil || strings.IndexByte(string(raw), 0) >= 0 {
			return nil
		}
		hits := Detect(string(raw))
		if len(hits) == 0 {
			return nil
		}
		if redact {
			d.Files[path] = FileBackup{Content: string(raw), Mode: info.Mode()}
			masked := MaskText(string(raw))
			output, openErr := safeRoot.OpenFile(rel, os.O_WRONLY|os.O_TRUNC, info.Mode())
			if openErr != nil {
				return openErr
			}
			_, e = io.WriteString(output, masked)
			closeErr := output.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		}
		for k, val := range hits {
			name := rel + "." + k
			d.Credentials[name] = Credential{Value: val, Source: path, CreatedAt: time.Now().UTC()}
			found[name] = val
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan directory: %w", err)
	}
	d.LastScan = time.Now().UTC()
	if err = v.saveUnlocked(d); err != nil {
		return nil, err
	}
	return found, v.auditUnlocked(AuditEntry{Action: "scan", Purpose: root})
}

// RedactFile masks one file and keeps its original bytes in the encrypted vault.
func (v *Vault) RedactFile(path string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve redaction target: %w", err)
	}
	root, err := os.OpenRoot(filepath.Dir(absPath))
	if err != nil {
		return fmt.Errorf("open redaction directory: %w", err)
	}
	defer root.Close()
	name := filepath.Base(absPath)
	file, err := root.Open(name)
	if err != nil {
		return fmt.Errorf("read redaction target: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	_ = file.Close()
	if err != nil {
		return fmt.Errorf("read redaction target: %w", err)
	}
	info, err := root.Stat(name)
	if err != nil {
		return fmt.Errorf("stat redaction target: %w", err)
	}
	d, err := v.loadUnlocked()
	if err != nil {
		return err
	}
	d.Files[absPath] = FileBackup{Content: string(raw), Mode: info.Mode()}
	output, err := root.OpenFile(name, os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("open redaction target: %w", err)
	}
	_, err = io.WriteString(output, MaskText(string(raw)))
	closeErr := output.Close()
	if err != nil {
		return fmt.Errorf("write redaction target: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close redaction target: %w", closeErr)
	}
	return v.saveUnlocked(d)
}
func (v *Vault) Restore() (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	d, err := v.loadUnlocked()
	if err != nil {
		return 0, err
	}
	paths := make([]string, 0, len(d.Files))
	for p := range d.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		b := d.Files[p]
		if err = os.WriteFile(p, []byte(b.Content), b.Mode); err != nil {
			return 0, fmt.Errorf("restore %s: %w", p, err)
		}
	}
	n := len(paths)
	d.Files = map[string]FileBackup{}
	if err = v.saveUnlocked(d); err != nil {
		return 0, err
	}
	return n, v.auditUnlocked(AuditEntry{Action: "restore"})
}
