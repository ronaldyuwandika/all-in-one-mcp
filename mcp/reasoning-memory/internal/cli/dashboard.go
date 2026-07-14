package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/prompter"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
)

var (
	subtle    = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7B59D9"}

	tabStyle    = lipgloss.NewStyle().Padding(0, 2).Foreground(subtle)
	activeTab   = lipgloss.NewStyle().Padding(0, 2).Foreground(highlight).Bold(true)
	detailStyle = lipgloss.NewStyle().Padding(1, 2).Foreground(subtle)
	headerStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(highlight)
)

type polishEntry struct {
	original string
	result   *prompter.PolishResult
}

type model struct {
	es      *store.EpisodeStore
	cfgPath string
	cfg     *models.Config

	width  int
	height int

	activeTab int
	tabNames  []string

	help  help.Model
	keys  keyMap
	ready bool

	epTable  table.Model
	episodes []models.EpisodeSummary

	patTable table.Model
	patterns []models.Pattern

	searchInput   textinput.Model
	searchResults []models.EpisodeSummary

	polishInput   textinput.Model
	polishResult  *prompter.PolishResult
	polishHistory []polishEntry

	showDetail bool
	detailVP   viewport.Model

	consolidationMsg string

	statsData *models.StatsResult

	errMsg string
}

type keyMap struct {
	Quit     key.Binding
	Tab      key.Binding
	ShiftTab key.Binding
	Enter    key.Binding
	Back     key.Binding
	Delete   key.Binding
	Help     key.Binding
	Paste    key.Binding
	Edit     key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Tab, k.ShiftTab, k.Enter, k.Back},
		{k.Delete, k.Paste, k.Edit, k.Help, k.Quit},
	}
}

func NewDashboardCmd(es *store.EpisodeStore, cfgPath string, cfg *models.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Launch the reasoning-memory TUI dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := tea.NewProgram(initialModel(es, cfgPath, cfg), tea.WithAltScreen())
			_, err := p.Run()
			return err
		},
	}
}

func initialModel(es *store.EpisodeStore, cfgPath string, cfg *models.Config) model {
	epColumns := []table.Column{
		{Title: "ID", Width: 18},
		{Title: "Domain", Width: 10},
		{Title: "Outcome", Width: 10},
		{Title: "Tags", Width: 20},
		{Title: "Duration", Width: 10},
	}
	epTable := table.New(table.WithColumns(epColumns), table.WithFocused(true))

	patColumns := []table.Column{
		{Title: "ID", Width: 22},
		{Title: "Domain", Width: 10},
		{Title: "Score", Width: 8},
		{Title: "Sources", Width: 10},
	}
	patTable := table.New(table.WithColumns(patColumns), table.WithFocused(true))

	si := textinput.New()
	si.Placeholder = "Type a search query..."
	si.Width = 50

	pi := textinput.New()
	pi.Placeholder = "Paste a raw prompt to polish..."
	pi.Width = 80

	dvp := viewport.New(80, 20)
	dvp.Style = lipgloss.NewStyle().Padding(0, 1)

	return model{
		es:      es,
		cfgPath: cfgPath,
		cfg:     cfg,
		tabNames: []string{
			"Episodes", "Patterns", "Search",
			"Consolidation", "Polish", "Stats",
		},
		epTable:     epTable,
		patTable:    patTable,
		searchInput: si,
		polishInput: pi,
		detailVP:    dvp,
		help:        help.New(),
		keys: keyMap{
			Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c", "ctrl+q", "ctrl+w"), key.WithHelp("q/⌘Q/⌘W", "quit")),
			Tab:      key.NewBinding(key.WithKeys("tab", "ctrl+tab"), key.WithHelp("tab/⌃tab", "next tab")),
			ShiftTab: key.NewBinding(key.WithKeys("shift+tab", "ctrl+shift+tab"), key.WithHelp("⇧tab/⌃⇧tab", "prev tab")),
			Enter:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select / open")),
			Back:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
			Delete:   key.NewBinding(key.WithKeys("d", "backspace"), key.WithHelp("d/⌫", "delete")),
			Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
			Paste:    key.NewBinding(key.WithKeys("ctrl+v"), key.WithHelp("⌘V", "paste")),
			Edit:     key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit in $EDITOR")),
		},
		consolidationMsg: "Press [c] to find merge candidates",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.loadEpisodes(),
		m.loadPatterns(),
	)
}

func (m model) loadEpisodes() tea.Cmd {
	return func() tea.Msg {
		eps, err := m.es.ListEpisodes(100, 0)
		if err != nil {
			return loadEpisodesMsg{nil, err}
		}
		return loadEpisodesMsg{eps, nil}
	}
}

func (m model) loadPatterns() tea.Cmd {
	return func() tea.Msg {
		pats, err := m.es.ListPatterns()
		if err != nil {
			return loadPatternsMsg{nil, err}
		}
		return loadPatternsMsg{pats, nil}
	}
}

type loadEpisodesMsg struct {
	episodes []models.EpisodeSummary
	err      error
}

type loadPatternsMsg struct {
	patterns []models.Pattern
	err      error
}

type errorMsg string

type polishResultMsg struct {
	result *prompter.PolishResult
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.detailVP.Width = msg.Width - 4
		m.detailVP.Height = msg.Height - 10
		m.epTable.SetWidth(msg.Width - 4)
		m.patTable.SetWidth(msg.Width - 4)
		m.ready = true

	case tea.KeyMsg:
		m.errMsg = ""

		if m.showDetail {
			switch {
			case key.Matches(msg, m.keys.Back):
				m.showDetail = false
				return m, nil
			case key.Matches(msg, m.keys.Quit):
				return m, tea.Quit
			default:
				var cmd tea.Cmd
				m.detailVP, cmd = m.detailVP.Update(msg)
				return m, cmd
			}
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
		case key.Matches(msg, m.keys.Tab):
			m.blurActiveInput()
			m.activeTab = (m.activeTab + 1) % len(m.tabNames)
			cmds = append(cmds, m.focusActiveInput()...)
			if m.activeTab == 3 {
				m.refreshConsolidation()
			}
			if m.activeTab == 5 {
				cmds = append(cmds, m.loadStats())
			}
		case key.Matches(msg, m.keys.ShiftTab):
			m.blurActiveInput()
			m.activeTab = (m.activeTab - 1 + len(m.tabNames)) % len(m.tabNames)
			cmds = append(cmds, m.focusActiveInput()...)
		}

		if m.activeTab == 0 {
			switch {
			case key.Matches(msg, m.keys.Enter):
				if len(m.episodes) > 0 && m.epTable.Cursor() < len(m.episodes) {
					ep := m.episodes[m.epTable.Cursor()]
					return m, m.loadEpisodeDetail(ep.ID)
				}
			case key.Matches(msg, m.keys.Delete):
				if len(m.episodes) > 0 && m.epTable.Cursor() < len(m.episodes) {
					ep := m.episodes[m.epTable.Cursor()]
					return m, m.deleteEpisode(ep.ID)
				}
			default:
				var cmd tea.Cmd
				m.epTable, cmd = m.epTable.Update(msg)
				return m, cmd
			}
		}

		if m.activeTab == 1 {
			switch {
			case key.Matches(msg, m.keys.Enter):
				if len(m.patterns) > 0 && m.patTable.Cursor() < len(m.patterns) {
					pat := m.patterns[m.patTable.Cursor()]
					return m, m.loadPatternDetail(pat.ID)
				}
			case key.Matches(msg, m.keys.Delete):
				if len(m.patterns) > 0 && m.patTable.Cursor() < len(m.patterns) {
					return m, m.deletePattern(m.patterns[m.patTable.Cursor()].ID)
				}
			default:
				var cmd tea.Cmd
				m.patTable, cmd = m.patTable.Update(msg)
				return m, cmd
			}
		}

		if m.activeTab == 2 {
			if msg.Type == tea.KeyEnter && m.searchInput.Focused() {
				return m, m.runSearch()
			}
			if msg.Type == tea.KeyEnter && !m.searchInput.Focused() && len(m.searchResults) > 0 {
				return m, m.loadEpisodeDetail(m.searchResults[0].ID)
			}
			if key.Matches(msg, m.keys.Back) {
				m.searchResults = nil
			}
		}

		if m.activeTab == 4 {
			if msg.Type == tea.KeyEnter && m.polishInput.Focused() {
				return m, m.runPolish()
			}
			if key.Matches(msg, m.keys.Back) {
				m.polishResult = nil
			}
			if key.Matches(msg, m.keys.Edit) {
				return m, m.editInEditor()
			}
		}

		if m.activeTab == 3 {
			switch msg.String() {
			case "c":
				return m, m.runConsolidate()
			case "p":
				return m, m.runPrune()
			case "r":
				return m, m.runReindex()
			}
		}

	case loadEpisodesMsg:
		if msg.err == nil {
			m.episodes = msg.episodes
			m.refreshEpTable()
		}
	case loadPatternsMsg:
		if msg.err == nil {
			m.patterns = msg.patterns
			m.refreshPatTable()
		}
	case episodeDetailMsg:
		if msg.err == nil {
			m.showDetail = true
			m.detailVP.SetContent(formatEpisode(msg.ep))
			m.detailVP.GotoTop()
		}
	case patternDetailMsg:
		if msg.err == nil {
			m.showDetail = true
			m.detailVP.SetContent(formatPattern(msg.pat))
			m.detailVP.GotoTop()
		}
	case deleteMsg:
		if msg.err == nil {
			return m, tea.Batch(m.loadEpisodes(), m.loadPatterns())
		}
		m.errMsg = fmt.Sprintf("Delete failed: %v", msg.err)
	case polishResultMsg:
		if msg.result == nil {
			m.errMsg = "Polish failed"
			break
		}
		m.polishResult = msg.result
		m.polishHistory = append(m.polishHistory, polishEntry{
			original: m.polishInput.Value(),
			result:   msg.result,
		})
		m.polishInput.SetValue("")
		m.polishInput.Blur()

		cmds = append(cmds, func() tea.Msg {
			_, err := m.es.CreateEpisode(&models.Episode{
				ID:            m.es.NextID(),
				Domain:        msg.result.Domain,
				Outcome:       "success",
				Tags:          []string{"polished_prompt", msg.result.TaskType},
				Problem:       "Polish prompt: " + msg.result.TaskType,
				ThinkingTrace: msg.result.PolishedPrompt,
			})
			if err != nil {
				return errorMsg(fmt.Sprintf("auto-capture: %v", err))
			}
			return nil
		})
	case searchResultsMsg:
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("Search failed: %v", msg.err)
			break
		}
		m.searchResults = msg.results
		m.searchInput.Blur()
	case errorMsg:
		m.errMsg = string(msg)
	case consolidateMsg:
		m.consolidationMsg = msg.report
	case statsMsg:
		m.statsData = msg.stats
	case editContentMsg:
		if msg.err != nil {
			m.errMsg = fmt.Sprintf("Editor: %v", msg.err)
			break
		}
		m.polishInput.SetValue(msg.content)
	}

	if m.activeTab == 2 && m.searchInput.Focused() {
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	if m.activeTab == 4 && m.polishInput.Focused() {
		var cmd tea.Cmd
		m.polishInput, cmd = m.polishInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *model) refreshEpTable() {
	var rows []table.Row
	for _, ep := range m.episodes {
		tags := strings.Join(ep.Tags, ",")
		if len(tags) > 18 {
			tags = tags[:18] + ".."
		}
		dur := fmt.Sprintf("%ds", ep.DurationSeconds)
		rows = append(rows, table.Row{
			ep.ID, ep.Domain, ep.Outcome, tags, dur,
		})
	}
	m.epTable.SetRows(rows)
}

func (m *model) refreshPatTable() {
	var rows []table.Row
	for _, pat := range m.patterns {
		rows = append(rows, table.Row{
			pat.ID, pat.Domain,
			fmt.Sprintf("%.3f", pat.MergeScore),
			fmt.Sprintf("%d", len(pat.Sources)),
		})
	}
	m.patTable.SetRows(rows)
}

func (m *model) blurActiveInput() {
	switch m.activeTab {
	case 2:
		m.searchInput.Blur()
	case 4:
		m.polishInput.Blur()
	}
}

func (m *model) focusActiveInput() []tea.Cmd {
	var cmds []tea.Cmd
	switch m.activeTab {
	case 2:
		cmds = append(cmds, m.searchInput.Focus())
	case 4:
		cmds = append(cmds, m.polishInput.Focus())
	}
	return cmds
}

func (m model) loadEpisodeDetail(id string) tea.Cmd {
	return func() tea.Msg {
		ep, err := m.es.GetEpisode(id)
		if err != nil {
			return episodeDetailMsg{nil, err}
		}
		return episodeDetailMsg{ep, nil}
	}
}

func (m model) loadPatternDetail(id string) tea.Cmd {
	return func() tea.Msg {
		pat, err := m.es.GetPattern(id)
		if err != nil {
			return patternDetailMsg{nil, err}
		}
		return patternDetailMsg{pat, nil}
	}
}

func (m model) deleteEpisode(id string) tea.Cmd {
	return func() tea.Msg {
		err := m.es.DeleteEpisode(id)
		return deleteMsg{err}
	}
}

func (m model) deletePattern(id string) tea.Cmd {
	return func() tea.Msg {
		return deleteMsg{m.es.DeletePattern(id)}
	}
}

type editContentMsg struct {
	content string
	err     error
}

func (m model) editInEditor() tea.Cmd {
	return func() tea.Msg {
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		f, err := os.CreateTemp("", "polish-*.md")
		if err != nil {
			return editContentMsg{"", err}
		}
		tmpPath := f.Name()
		if m.polishInput.Value() != "" {
			f.WriteString(m.polishInput.Value())
		}
		f.Close()
		defer os.Remove(tmpPath)

		cmd := exec.Command("/bin/sh", "-c", editor+" "+tmpPath)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return editContentMsg{"", err}
		}
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			return editContentMsg{"", err}
		}
		return editContentMsg{string(data), nil}
	}
}

func (m model) runSearch() tea.Cmd {
	return func() tea.Msg {
		q := m.searchInput.Value()
		if q == "" {
			return searchResultsMsg{nil, nil}
		}
		results, err := m.es.SearchLocal(q, "", "", nil, 20)
		if err != nil {
			return searchResultsMsg{nil, err}
		}
		return searchResultsMsg{results, nil}
	}
}

func (m model) runPolish() tea.Cmd {
	return func() tea.Msg {
		raw := m.polishInput.Value()
		if raw == "" {
			return polishResultMsg{nil}
		}
		result, err := prompter.PolishPrompt(raw, "", "", "", false)
		if err != nil {
			return polishResultMsg{nil}
		}
		return polishResultMsg{result}
	}
}

func (m model) runConsolidate() tea.Cmd {
	return func() tea.Msg {
		candidates, err := m.es.FindMergeCandidates(m.cfg.Consolidation.MinEpisodesForPattern)
		if err != nil {
			return consolidateMsg{fmt.Sprintf("Error: %v", err)}
		}
		var report strings.Builder
		fmt.Fprintf(&report, "Found %d merge candidates\n", len(candidates))
		for i, c := range candidates {
			if i >= 10 {
				fmt.Fprintf(&report, "... and %d more\n", len(candidates)-10)
				break
			}
			pid, err := m.es.MergeToPattern(c)
			if err != nil {
				fmt.Fprintf(&report, "  ⚠ %s+%s: %v\n", c.A, c.B, err)
			} else {
				fmt.Fprintf(&report, "  ✓ → %s (score=%.3f)\n", pid, c.Score)
			}
		}
		return consolidateMsg{report.String()}
	}
}

func (m model) runPrune() tea.Cmd {
	return func() tea.Msg {
		pruned, err := m.es.PruneFailures(m.cfg.Consolidation.PruneAfterDays)
		if err != nil {
			return consolidateMsg{fmt.Sprintf("Prune error: %v", err)}
		}
		return consolidateMsg{fmt.Sprintf("Pruned %d stale failure episodes", pruned)}
	}
}

func (m model) runReindex() tea.Cmd {
	return func() tea.Msg {
		if err := m.es.ReindexFTS5(); err != nil {
			return consolidateMsg{fmt.Sprintf("Reindex error: %v", err)}
		}
		count, _ := m.es.EpisodeCount()
		return consolidateMsg{fmt.Sprintf("FTS5 index rebuilt (%d episodes)", count)}
	}
}

type episodeDetailMsg struct {
	ep  *models.Episode
	err error
}

type patternDetailMsg struct {
	pat *models.Pattern
	err error
}

type deleteMsg struct {
	err error
}

type searchResultsMsg struct {
	results []models.EpisodeSummary
	err     error
}

type consolidateMsg struct {
	report string
}

type statsMsg struct {
	stats *models.StatsResult
}

func (m model) loadStats() tea.Cmd {
	return func() tea.Msg {
		epTotal, _ := m.es.EpisodeCount()
		patTotal, _ := m.es.PatternCount()
		byDomain, _ := m.es.EpisodesByDomain()
		byOutcome, _ := m.es.EpisodesByOutcome()
		topTags, _ := m.es.TopTags(10)
		avgProb, avgTrace, _ := m.es.AvgEpisodeLengths()
		dbSize, _ := m.es.DBSizeMB()
		ftsSize, _ := m.es.FTSSizeMB()
		lastCons, _ := m.es.LastConsolidationTS()
		summary, _ := m.es.SummaryStats()
		epByDay, _ := m.es.EpisodesByDay(7)

		var lc *string
		if lastCons != nil {
			s := lastCons.Format("2006-01-02 15:04")
			lc = &s
		}

		sr := &models.StatsResult{
			EpisodesTotal:         epTotal,
			PatternsTotal:         patTotal,
			EpisodesByDomain:      byDomain,
			EpisodesByOutcome:     byOutcome,
			TopTags:               topTags,
			DBSizeMB:              dbSize,
			FTSSizeMB:             ftsSize,
			ConsolidationsTotal:   patTotal,
			LastConsolidationTS:   lc,
			AvgEpisodeLenChars:    avgProb,
			AvgThinkingTraceChars: avgTrace,
		}
		if summary != nil {
			sr.SuccessRate = summary.SuccessRate
			sr.ConsolidationRatio = summary.ConsolidationRatio
			sr.TopDomain = summary.TopDomain
			sr.AvgDurationSec = summary.AvgDurationSec
		}
		if epByDay != nil {
			sr.EpisodesByDay = epByDay
		}
		return statsMsg{sr}
	}
}

func (m *model) refreshConsolidation() {
	epTotal, _ := m.es.EpisodeCount()
	patTotal, _ := m.es.PatternCount()
	lastCons, _ := m.es.LastConsolidationTS()

	var report strings.Builder
	fmt.Fprintf(&report, "Episodes: %d\n", epTotal)
	fmt.Fprintf(&report, "Patterns: %d\n", patTotal)
	if epTotal > 0 {
		fmt.Fprintf(&report, "Consolidation ratio: %.1f%%\n", float64(patTotal)/float64(epTotal)*100)
	}
	if lastCons != nil {
		fmt.Fprintf(&report, "Last consolidation: %s", lastCons.Format("2006-01-02 15:04"))
	}
	fmt.Fprint(&report, "\n\nKeys: [c] merge candidates  [p] prune  [r] rebuild FTS5 index")
	m.consolidationMsg = report.String()
}

func formatEpisode(ep *models.Episode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Episode: %s\n\n", ep.ID)
	fmt.Fprintf(&b, "Domain: %s  |  Outcome: %s  |  Duration: %ds\n", ep.Domain, ep.Outcome, ep.DurationSeconds)
	fmt.Fprintf(&b, "Model: %s\n", ep.ModelID)
	fmt.Fprintf(&b, "Created: %s\n", ep.CreatedAt.Format("2006-01-02 15:04:05"))
	if len(ep.Tags) > 0 {
		fmt.Fprintf(&b, "Tags: %s\n", strings.Join(ep.Tags, ", "))
	}
	fmt.Fprintf(&b, "\nProblem:\n%s\n", ep.Problem)
	fmt.Fprintf(&b, "\nThinking Trace:\n%s\n", ep.ThinkingTrace)
	if len(ep.ToolCalls) > 0 {
		fmt.Fprintf(&b, "\nTool Calls (%d):\n", len(ep.ToolCalls))
		for _, tc := range ep.ToolCalls {
			fmt.Fprintf(&b, "  \u2022 %s \u2192 %s\n", tc.Tool, tc.Outcome)
		}
	}
	return b.String()
}

func formatPattern(pat *models.Pattern) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pattern: %s\n\n", pat.ID)
	fmt.Fprintf(&b, "Domain: %s  |  Merge Score: %.3f\n", pat.Domain, pat.MergeScore)
	fmt.Fprintf(&b, "Sources: %s\n\n", strings.Join(pat.Sources, ", "))
	fmt.Fprintf(&b, "Consolidated Prompt:\n%s\n\n", pat.ConsolidatedPrompt)
	fmt.Fprintf(&b, "Master Thinking Path:\n%s\n", pat.MasterThinkingPath)
	return b.String()
}

func (m model) View() string {
	if !m.ready {
		return "Loading reasoning-memory dashboard..."
	}

	if m.showDetail {
		return m.detailView()
	}

	var b strings.Builder

	tabs := make([]string, len(m.tabNames))
	for i, name := range m.tabNames {
		if i == m.activeTab {
			tabs[i] = activeTab.Render(name)
		} else {
			tabs[i] = tabStyle.Render(name)
		}
	}
	fmt.Fprintln(&b, lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	fmt.Fprintln(&b, lipgloss.NewStyle().Padding(0, 1).Foreground(subtle).Render(strings.Repeat("─", m.width-2)))

	switch m.activeTab {
	case 0:
		b.WriteString(m.episodesView())
	case 1:
		b.WriteString(m.patternsView())
	case 2:
		b.WriteString(m.searchView())
	case 3:
		b.WriteString(m.consolidationView())
	case 4:
		b.WriteString(m.polishView())
	case 5:
		b.WriteString(m.statsView())
	}

	if m.errMsg != "" {
		fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("  ✗ "+m.errMsg))
	}
	fmt.Fprintln(&b)
	b.WriteString(m.help.View(m.keys))

	return b.String()
}

func (m model) detailView() string {
	var b strings.Builder
	fmt.Fprintln(&b, headerStyle.Render("Detail View"))
	fmt.Fprintln(&b, detailStyle.Render("Press esc to go back, q to quit"))
	fmt.Fprintln(&b, strings.Repeat("─", m.width-2))

	content := m.detailVP.View()
	b.WriteString(content)

	return b.String()
}

func (m model) episodesView() string {
	if len(m.episodes) == 0 {
		return "  No episodes found\n"
	}
	return m.epTable.View() + "\n  d: delete | enter: detail"
}

func (m model) patternsView() string {
	if len(m.patterns) == 0 {
		return "  No patterns found\n"
	}
	return m.patTable.View() + "\n  d: delete | enter: detail"
}

func (m model) searchView() string {
	var b strings.Builder
	b.WriteString(m.searchInput.View() + "  [enter: search]")
	b.WriteString("\n")

	if len(m.searchResults) > 0 {
		fmt.Fprintf(&b, "\n  %d result(s):\n", len(m.searchResults))
		for i, r := range m.searchResults {
			line := fmt.Sprintf("  %d. %s [%s/%s] %s", i+1, r.ID, r.Domain, r.Outcome, truncate(r.Problem, 60))
			b.WriteString(line + "\n")
			if i >= 19 {
				break
			}
		}
	} else if m.searchInput.Value() != "" {
		b.WriteString("  No results found\n")
	}

	return b.String()
}

func (m model) consolidationView() string {
	return "  " + strings.ReplaceAll(m.consolidationMsg, "\n", "\n  ") + "\n"
}

func (m model) polishView() string {
	var b strings.Builder
	b.WriteString(m.polishInput.View())
	b.WriteString("  [enter: polish | e: $EDITOR]\n")

	if m.polishResult != nil {
		fmt.Fprintf(&b, "\n  Type: %s  |  Domain: %s  |  Skill: %s\n",
			m.polishResult.TaskType, m.polishResult.Domain, m.polishResult.SkillName)
		fmt.Fprintf(&b, "  Language: %s  |  Context: %d\n\n",
			m.polishResult.Language, m.polishResult.ContextCount)
		b.WriteString("  Polished Prompt:\n  " + strings.ReplaceAll(m.polishResult.PolishedPrompt, "\n", "\n  ") + "\n")
	}

	if len(m.polishHistory) > 0 {
		fmt.Fprintf(&b, "\n  History (%d):\n", len(m.polishHistory))
		start := len(m.polishHistory) - 5
		if start < 0 {
			start = 0
		}
		for i := start; i < len(m.polishHistory); i++ {
			entry := m.polishHistory[i]
			fmt.Fprintf(&b, "  %d. %s → %s\n", i+1,
				truncate(entry.original, 40),
				entry.result.TaskType)
		}
	}

	return b.String()
}

func (m model) statsView() string {
	if m.statsData == nil {
		return "  Loading stats...\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  %-30s %s\n", "Metric", "Value")
	fmt.Fprintf(&b, "  %s\n", strings.Repeat("─", 50))
	fmt.Fprintf(&b, "  %-30s %d\n", "Episodes (total)", m.statsData.EpisodesTotal)
	fmt.Fprintf(&b, "  %-30s %d\n", "Patterns (total)", m.statsData.PatternsTotal)
	fmt.Fprintf(&b, "  %-30s %s\n", "Top domain", m.statsData.TopDomain)
	fmt.Fprintf(&b, "  %-30s %.1f%%\n", "Success rate", m.statsData.SuccessRate*100)
	fmt.Fprintf(&b, "  %-30s %.1f%%\n", "Consolidation ratio", m.statsData.ConsolidationRatio*100)
	fmt.Fprintf(&b, "  %-30s %.1f s\n", "Avg duration", m.statsData.AvgDurationSec)
	maybeNA := func(v float64) string {
		if v == 0 {
			return "N/A"
		}
		return fmt.Sprintf("%.0f", v)
	}
	fmt.Fprintf(&b, "  %-30s %s\n", "Avg episode length (chars)", maybeNA(m.statsData.AvgEpisodeLenChars))
	fmt.Fprintf(&b, "  %-30s %s\n", "Avg trace length (chars)", maybeNA(m.statsData.AvgThinkingTraceChars))

	if m.statsData.EpisodesByDomain != nil {
		fmt.Fprintf(&b, "\n  By Domain:\n")
		for domain, count := range m.statsData.EpisodesByDomain {
			fmt.Fprintf(&b, "    %-20s %d\n", domain, count)
		}
	}
	if m.statsData.EpisodesByOutcome != nil {
		fmt.Fprintf(&b, "\n  By Outcome:\n")
		for outcome, count := range m.statsData.EpisodesByOutcome {
			fmt.Fprintf(&b, "    %-20s %d\n", outcome, count)
		}
	}

	fmt.Fprintf(&b, "\n  %-30s %.1f MB\n", "DB size", m.statsData.DBSizeMB)
	fmt.Fprintf(&b, "  %-30s %.1f MB\n", "FTS5 index", m.statsData.FTSSizeMB)

	if m.statsData.LastConsolidationTS != nil {
		fmt.Fprintf(&b, "  %-30s %s\n", "Last consolidation", *m.statsData.LastConsolidationTS)
	}

	if len(m.statsData.EpisodesByDay) > 0 {
		fmt.Fprintf(&b, "\n  Last 7 Days:\n")
		for _, d := range m.statsData.EpisodesByDay {
			fmt.Fprintf(&b, "    %s: %d eps, %d ok, %.0f s avg\n", d.Date, d.Count, d.Successes, d.AvgDuration)
		}
	}

	if len(m.statsData.TopTags) > 0 {
		fmt.Fprintf(&b, "\n  Top Tags:\n")
		for _, tc := range m.statsData.TopTags {
			fmt.Fprintf(&b, "    %-20s %d\n", tc.Tag, tc.Count)
		}
	}

	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
