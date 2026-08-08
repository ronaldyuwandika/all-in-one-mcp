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

	vaultcrypto "github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault/internal/crypto"
	"github.com/ronaldyuwandika/all-in-one-mcp/pkg/secretdetect"
)

var ErrNotFound = errors.New("credential not found")

const (
	maxVaultBytes      = 256 << 20
	maxFileBackupBytes = 64 << 20
	maxScanFileBytes   = 2 << 20
)

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
	ScanRoots   []string              `json:"scan_roots,omitempty"`
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
	return New(filepath.Join(home, ".credential-vault"), vaultcrypto.New(vaultcrypto.SystemKeyStore{})), nil
}
func (v *Vault) vaultPath() string { return filepath.Join(v.dir, "vault.json") }
func (v *Vault) AuditPath() string { return filepath.Join(v.dir, "audit.jsonl") }

func emptyData() Data {
	return Data{Credentials: map[string]Credential{}, Files: map[string]FileBackup{}, CreatedAt: time.Now().UTC()}
}
func (v *Vault) loadUnlocked() (Data, error) {
	info, err := os.Stat(v.vaultPath())
	if errors.Is(err, os.ErrNotExist) {
		return emptyData(), nil
	}
	if err != nil {
		return Data{}, fmt.Errorf("stat vault: %w", err)
	}
	if info.Size() > maxVaultBytes {
		return Data{}, fmt.Errorf("vault file exceeds %d bytes", maxVaultBytes)
	}
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
	if len(token) > maxVaultBytes {
		return fmt.Errorf("vault data exceeds %d bytes", maxVaultBytes)
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
func (v *Vault) DetectDir(root string) (map[string]string, error) {
	return v.scanDir(root, false)
}

func (v *Vault) ScanDir(root string, redact bool) (map[string]string, error) {
	return v.scanDir(root, redact)
}

func (v *Vault) RedactDir(root string) (map[string]string, error) {
	return v.scanDir(root, true)
}

func (v *Vault) scanDir(root string, redact bool) (map[string]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	d := emptyData()
	if redact {
		var err error
		d, err = v.loadUnlocked()
		if err != nil {
			return nil, err
		}
	}
	found := map[string]string{}
	maskedFiles := map[string]string{}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve scan root: %w", err)
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve scan root symlinks: %w", err)
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
		if redact && isVaultInternalPath(path, v.dir) {
			return nil
		}
		info, e := safeRoot.Lstat(rel)
		if e != nil || !info.Mode().IsRegular() || info.Size() > maxScanFileBytes {
			return nil
		}
		file, e := safeRoot.Open(rel)
		if e != nil {
			return nil
		}
		raw, e := io.ReadAll(io.LimitReader(file, maxScanFileBytes+1))
		closeErr := file.Close()
		if e != nil || closeErr != nil || len(raw) > maxScanFileBytes || strings.IndexByte(string(raw), 0) >= 0 {
			return nil
		}
		hits := Detect(string(raw))
		if len(hits) == 0 {
			return nil
		}
		if redact {
			if len(raw) > maxFileBackupBytes {
				return nil
			}
			d.Files[path] = FileBackup{Content: string(raw), Mode: info.Mode()}
			maskedFiles[path] = MaskText(string(raw))
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
	if !redact {
		return found, nil
	}
	d.LastScan = time.Now().UTC()
	d.ScanRoots = appendUniqueRoot(d.ScanRoots, absRoot)
	if err = v.saveUnlocked(d); err != nil {
		return nil, err
	}
	for path, masked := range maskedFiles {
		rel, e := filepath.Rel(absRoot, path)
		if e != nil {
			return nil, e
		}
		tmp, e := os.CreateTemp(absRoot, ".credential-vault-redact-*")
		if e != nil {
			return nil, e
		}
		tmpName := tmp.Name()
		if e = tmp.Chmod(d.Files[path].Mode.Perm()); e == nil {
			_, e = io.WriteString(tmp, masked)
		}
		if e == nil {
			e = tmp.Sync()
		}
		closeErr := tmp.Close()
		if e == nil {
			e = closeErr
		}
		if e == nil {
			e = safeRoot.Rename(filepath.Base(tmpName), rel)
		}
		if e != nil {
			_ = os.Remove(tmpName)
			return nil, e
		}
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
	info, err := root.Lstat(name)
	if err != nil {
		return fmt.Errorf("stat redaction target: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("redaction target is not a regular file")
	}
	if info.Size() > maxScanFileBytes {
		return fmt.Errorf("redaction target exceeds %d bytes", maxScanFileBytes)
	}
	file, err := root.Open(name)
	if err != nil {
		return fmt.Errorf("read redaction target: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxScanFileBytes+1))
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("read redaction target: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close redaction target: %w", closeErr)
	}
	if len(raw) > maxScanFileBytes {
		return fmt.Errorf("redaction target exceeds %d bytes", maxScanFileBytes)
	}
	d, err := v.loadUnlocked()
	if err != nil {
		return err
	}
	d.Files[absPath] = FileBackup{Content: string(raw), Mode: info.Mode()}
	d.ScanRoots = appendUniqueRoot(d.ScanRoots, filepath.Dir(absPath))
	output, err := root.OpenFile(name, os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("open redaction target: %w", err)
	}
	_, err = io.WriteString(output, MaskText(string(raw)))
	closeErr = output.Close()
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
	if len(d.ScanRoots) == 0 && len(paths) > 0 {
		return 0, errors.New("restore refused: encrypted backups have no approved roots; re-redact files with the current vault")
	}
	targets := make(map[string]restoreTarget, len(paths))
	for _, p := range paths {
		targets[p], err = validateRestoreFile(p, d.ScanRoots)
		if err != nil {
			return 0, fmt.Errorf("validate restore %s: %w", p, err)
		}
	}
	for i, p := range paths {
		if err = restoreFile(targets[p], d.Files[p]); err != nil {
			return i, fmt.Errorf("restored %d files before %s: %w", i, p, err)
		}
	}
	n := len(paths)
	d.Files = map[string]FileBackup{}
	if err = v.saveUnlocked(d); err != nil {
		return n, fmt.Errorf("restored %d files but could not save vault: %w", n, err)
	}
	if err = v.auditUnlocked(AuditEntry{Action: "restore"}); err != nil {
		return n, fmt.Errorf("restored %d files but could not audit restore: %w", n, err)
	}
	return n, nil
}

func appendUniqueRoot(roots []string, root string) []string {
	for _, existing := range roots {
		if existing == root {
			return roots
		}
	}
	return append(roots, root)
}

func withinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "."
}

func isVaultInternalPath(path, vaultDir string) bool {
	path = filepath.Clean(path)
	if strings.HasPrefix(filepath.Base(path), ".credential-vault-") {
		return true
	}
	absVaultDir, err := filepath.Abs(vaultDir)
	if err != nil {
		return false
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absVaultDir); resolveErr == nil {
		absVaultDir = resolved
	}
	return withinRoot(absVaultDir, path) || path == filepath.Join(absVaultDir, "vault.json") || path == filepath.Join(absVaultDir, "audit.jsonl")
}

type restoreTarget struct {
	dir  string
	name string
}

func validateRestoreFile(path string, roots []string) (restoreTarget, error) {
	absPath, err := filepath.Abs(path)
	if err != nil || absPath != filepath.Clean(path) || filepath.Base(absPath) == "." {
		return restoreTarget{}, errors.New("legacy backup path is not an approved absolute file path")
	}
	realDir, err := filepath.EvalSymlinks(filepath.Dir(absPath))
	if err != nil {
		return restoreTarget{}, fmt.Errorf("resolve restore directory: %w", err)
	}
	realPath := filepath.Join(realDir, filepath.Base(absPath))
	approved := false
	for _, root := range roots {
		absRoot, rootErr := filepath.Abs(root)
		if rootErr != nil {
			continue
		}
		realRoot, rootErr := filepath.EvalSymlinks(absRoot)
		if rootErr == nil && withinRoot(realRoot, realPath) {
			approved = true
			break
		}
	}
	if !approved {
		return restoreTarget{}, errors.New("restore target is outside approved scan roots")
	}
	root, err := os.OpenRoot(realDir)
	if err != nil {
		return restoreTarget{}, fmt.Errorf("open restore directory: %w", err)
	}
	defer root.Close()
	name := filepath.Base(realPath)
	if info, statErr := root.Lstat(name); statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return restoreTarget{}, statErr
	} else if statErr == nil && !info.Mode().IsRegular() {
		return restoreTarget{}, errors.New("restore target is not a regular file")
	}
	return restoreTarget{dir: realDir, name: name}, nil
}

func restoreFile(target restoreTarget, backup FileBackup) error {
	root, err := os.OpenRoot(target.dir)
	if err != nil {
		return fmt.Errorf("open restore directory: %w", err)
	}
	defer root.Close()
	if info, statErr := root.Lstat(target.name); statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	} else if statErr == nil && !info.Mode().IsRegular() {
		return errors.New("restore target is not a regular file")
	}
	tmp, err := os.CreateTemp(target.dir, ".credential-vault-restore-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(backup.Mode.Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.WriteString(backup.Content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return root.Rename(filepath.Base(tmpName), target.name)
}

func (v *Vault) Compact() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	raw, err := os.ReadFile(v.vaultPath())
	if err != nil {
		return fmt.Errorf("read vault for compaction: %w", err)
	}
	plain, err := v.crypt.Decrypt(strings.TrimSpace(string(raw)))
	if err != nil {
		return fmt.Errorf("decrypt vault for compaction: %w", err)
	}
	var data Data
	if err = json.Unmarshal(plain, &data); err != nil {
		return fmt.Errorf("decode vault for compaction: %w", err)
	}
	data.Files = map[string]FileBackup{}
	return v.saveUnlocked(data)
}
