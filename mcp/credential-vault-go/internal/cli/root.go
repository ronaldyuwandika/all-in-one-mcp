// Package cli defines the vaultctl command tree.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/config"
	vaultcrypto "github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/crypto"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/tui"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/vault"
	"github.com/spf13/cobra"
)

func NewRoot() *cobra.Command {
	var cfgPath string
	root := &cobra.Command{Use: "vaultctl", Short: "Local encrypted credential vault", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&cfgPath, "config", "", "config file")
	open := func() (*vault.Vault, error) {
		c, err := config.Load(cfgPath)
		if err != nil {
			return nil, err
		}
		if d := os.Getenv("CREDENTIAL_VAULT_DIR"); d != "" {
			c.VaultDir = d
		}
		return vault.New(expand(c.VaultDir), newCrypt()), nil
	}
	root.AddCommand(statusCmd(open), getCmd(open), setCmd(open), scanCmd(open), restoreCmd(open), auditCmd(open), statsCmd(open), doctorCmd(open), dashboardCmd(open), exportCmd(open), importCmd(open), clearCmd(open))
	return root
}

func newCrypt() *vaultcrypto.Fernet { return vaultcrypto.New(vaultcrypto.SystemKeyStore{}) }
func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		return h + p[1:]
	}
	return p
}

type opener func() (*vault.Vault, error)

func statusCmd(o opener) *cobra.Command {
	return &cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		v, e := o()
		if e != nil {
			return e
		}
		d, e := v.Load()
		if e != nil {
			return e
		}
		names := make([]string, 0, len(d.Credentials))
		for n := range d.Credentials {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintln(c.OutOrStdout(), n)
		}
		return nil
	}}
}
func getCmd(o opener) *cobra.Command {
	var purpose string
	var quiet bool
	cmd := &cobra.Command{Use: "get NAME", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
		v, e := o()
		if e != nil {
			return e
		}
		value, e := v.Get(a[0], purpose)
		if e != nil {
			return e
		}
		if quiet {
			_, e = fmt.Fprint(c.OutOrStdout(), value)
		} else {
			_, e = fmt.Fprintln(c.OutOrStdout(), value)
		}
		return e
	}}
	cmd.Flags().StringVarP(&purpose, "purpose", "p", "", "audit purpose")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "omit trailing newline")
	_ = cmd.MarkFlagRequired("purpose")
	return cmd
}
func setCmd(o opener) *cobra.Command {
	return &cobra.Command{Use: "set NAME [VALUE]", Args: cobra.RangeArgs(1, 2), RunE: func(c *cobra.Command, a []string) error {
		var value string
		if len(a) == 2 {
			value = a[1]
		} else {
			raw, e := io.ReadAll(c.InOrStdin())
			if e != nil {
				return e
			}
			value = strings.TrimSpace(string(raw))
		}
		v, e := o()
		if e != nil {
			return e
		}
		return v.Set("chat."+a[0], value, "cli")
	}}
}
func scanCmd(o opener) *cobra.Command {
	var noRedact bool
	cmd := &cobra.Command{Use: "scan [PATH]", Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, a []string) error {
		root := "."
		if len(a) > 0 {
			root = a[0]
		}
		v, e := o()
		if e != nil {
			return e
		}
		found, e := v.ScanDir(root, !noRedact)
		if e == nil {
			fmt.Fprintf(c.OutOrStdout(), "found %d credentials\n", len(found))
		}
		return e
	}}
	cmd.Flags().BoolVar(&noRedact, "no-redact", false, "detect without rewriting files")
	return cmd
}
func restoreCmd(o opener) *cobra.Command {
	return &cobra.Command{Use: "restore", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		v, e := o()
		if e != nil {
			return e
		}
		n, e := v.Restore()
		if e == nil {
			fmt.Fprintf(c.OutOrStdout(), "restored %d files\n", n)
		}
		return e
	}}
}
func auditCmd(o opener) *cobra.Command {
	var limit int
	cmd := &cobra.Command{Use: "audit", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		v, e := o()
		if e != nil {
			return e
		}
		a, e := v.Audit(limit)
		if e != nil {
			return e
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(a)
	}}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum entries")
	return cmd
}
func statsCmd(o opener) *cobra.Command {
	var format string
	cmd := &cobra.Command{Use: "stats", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		v, e := o()
		if e != nil {
			return e
		}
		s, e := v.Stats()
		if e != nil {
			return e
		}
		if format == "json" {
			return json.NewEncoder(c.OutOrStdout()).Encode(s)
		}
		fmt.Fprintf(c.OutOrStdout(), "Credentials: %d (file=%d chat=%d)\nRedacted files: %d\nAudit entries: %d\nVault bytes: %d\n", s.TotalCredentials, s.FileCredentials, s.ChatCredentials, s.RedactedFilesCount, s.AuditEntriesTotal, s.VaultFileSizeBytes)
		return nil
	}}
	cmd.Flags().StringVar(&format, "format", "table", "table or json")
	return cmd
}

type doctorError struct{ code int }

func (e doctorError) Error() string { return fmt.Sprintf("doctor found problems (exit %d)", e.code) }
func doctorCmd(o opener) *cobra.Command {
	var format string
	cmd := &cobra.Command{Use: "doctor", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		v, e := o()
		if e != nil {
			return e
		}
		r := v.Doctor()
		if format == "json" {
			e = json.NewEncoder(c.OutOrStdout()).Encode(r)
		} else {
			for _, x := range r.Checks {
				fmt.Fprintf(c.OutOrStdout(), "%-8s %-24s %s\n", x.Status, x.Name, x.Message)
			}
		}
		if e != nil {
			return e
		}
		if r.ExitCode > 0 {
			return doctorError{r.ExitCode}
		}
		return nil
	}}
	cmd.Flags().StringVar(&format, "format", "table", "table or json")
	return cmd
}
func dashboardCmd(o opener) *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{Use: "dashboard", Aliases: []string{"watch"}, Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		v, e := o()
		if e != nil {
			return e
		}
		return tui.Run(v, interval)
	}}
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "refresh interval")
	return cmd
}
func exportCmd(o opener) *cobra.Command {
	return &cobra.Command{Use: "export FILE", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		v, e := o()
		if e != nil {
			return e
		}
		return v.Export(a[0])
	}}
}
func importCmd(o opener) *cobra.Command {
	return &cobra.Command{Use: "import FILE", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		v, e := o()
		if e != nil {
			return e
		}
		return v.Import(a[0])
	}}
}
func clearCmd(o opener) *cobra.Command {
	return &cobra.Command{Use: "chat-clear", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		v, e := o()
		if e != nil {
			return e
		}
		n, e := v.ClearChat()
		if e == nil {
			fmt.Fprintln(c.OutOrStdout(), n)
		}
		return e
	}}
}
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var d doctorError
	if errors.As(err, &d) {
		return d.code
	}
	return 1
}
