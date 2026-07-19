// Package tui implements the interactive credential-vault dashboard.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/vault"
)

type tickMsg time.Time
type Model struct {
	vault    *vault.Vault
	interval time.Duration
	tab      int
	body     string
	err      error
}

func New(v *vault.Vault, interval time.Duration) Model {
	m := Model{vault: v, interval: interval}
	m.refresh()
	return m
}
func (m Model) Init() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.KeyMsg:
		switch x.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "right":
			m.tab = (m.tab + 1) % 4
		case "shift+tab", "left":
			m.tab = (m.tab + 3) % 4
		case "s":
			_, m.err = m.vault.ScanDir(".", true)
		case "r":
			_, m.err = m.vault.Restore()
		case "e":
			m.err = m.vault.Export("credential-vault-export.json")
		}
		m.refresh()
	case tickMsg:
		m.refresh()
		return m, tea.Tick(m.interval, func(t time.Time) tea.Msg { return tickMsg(t) })
	}
	return m, nil
}
func (m *Model) refresh() {
	names := []string{"Credentials", "Audit Log", "File Status", "System"}
	var b strings.Builder
	fmt.Fprintf(&b, "Credential Vault Dashboard — %s\n\n", names[m.tab])
	switch m.tab {
	case 0:
		d, e := m.vault.Load()
		m.err = e
		for n := range d.Credentials {
			fmt.Fprintln(&b, n)
		}
	case 1:
		a, e := m.vault.Audit(20)
		m.err = e
		for _, x := range a {
			fmt.Fprintf(&b, "%s %-8s %s %s\n", x.Timestamp.Format(time.RFC3339), x.Action, x.Credential, x.Purpose)
		}
	case 2:
		d, e := m.vault.Load()
		m.err = e
		for p := range d.Files {
			fmt.Fprintln(&b, p)
		}
	case 3:
		s, e := m.vault.Stats()
		m.err = e
		fmt.Fprintf(&b, "Credentials: %d\nVault bytes: %d\nAudit entries: %d\nKeychain accessible: %t\n", s.TotalCredentials, s.VaultFileSizeBytes, s.AuditEntriesTotal, s.KeychainAccessible)
	}
	m.body = b.String()
}
func (m Model) View() string {
	errText := ""
	if m.err != nil {
		errText = "\nError: " + m.err.Error()
	}
	return m.body + errText + "\n\n[tab] next  [s] scan  [r] restore  [e] export  [q] quit\n"
}
func Run(v *vault.Vault, interval time.Duration) error {
	_, err := tea.NewProgram(New(v, interval), tea.WithAltScreen()).Run()
	return err
}
