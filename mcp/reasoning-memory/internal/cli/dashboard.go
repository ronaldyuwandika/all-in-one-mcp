package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/prompter"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
)

// Color palette matched to visualization.html (GitHub dark theme)
var (
	ghSubtle    = lipgloss.Color("#8b949e") // visualization.html subtle text
	ghHighlight = lipgloss.Color("#58a6ff") // visualization.html blue accent
	ghBright    = lipgloss.Color("#f0f6fc") // visualization.html bright text
	ghOrange    = lipgloss.Color("#f78166") // visualization.html active tab underline
	ghBorder    = lipgloss.Color("#30363d") // visualization.html border color
	ghDarkBg    = lipgloss.Color("#21262d") // visualization.html active tab background
	ghGreen     = lipgloss.Color("#238636") // visualization.html badge-success
	ghRed       = lipgloss.Color("#da3633") // visualization.html badge-fail
	ghPurple    = lipgloss.Color("#d2a8ff") // visualization.html pattern count
	ghBrightGrn = lipgloss.Color("#3fb950") // visualization.html success rate

	headerStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(ghHighlight)
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

	conTable table.Model
	concepts []store.SemanticConcept

	graphTable table.Model
	graphEdges []store.GraphEdge

	searchInput   textinput.Model
	repoInput     textinput.Model
	searchResults []models.EpisodeSummary

	polishInput   textarea.Model
	polishResult  *prompter.PolishResult
	polishHistory []polishEntry

	showDetail    bool
	detailFocused bool
	detailVP      viewport.Model
	detailRaw     string
	polishVP      viewport.Model

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
	Promote  key.Binding
	Traverse key.Binding
	Polish   key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Quit, k.Polish}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Tab, k.ShiftTab, k.Enter, k.Back, k.Polish},
		{k.Delete, k.Paste, k.Edit, k.Promote, k.Traverse, k.Help, k.Quit},
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
		{Title: "Outcome", Width: 20}, // increased to prevent truncation of ANSI colors
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

	conColumns := []table.Column{
		{Title: "ID", Width: 18},
		{Title: "Entity", Width: 15},
		{Title: "Type", Width: 12},
		{Title: "Description", Width: 35},
		{Title: "Accesses", Width: 10},
	}
	conTable := table.New(table.WithColumns(conColumns), table.WithFocused(true))

	graphColumns := []table.Column{
		{Title: "Source", Width: 20},
		{Title: "Target", Width: 20},
		{Title: "Relation", Width: 15},
		{Title: "Weight", Width: 8},
	}
	graphTable := table.New(table.WithColumns(graphColumns), table.WithFocused(true))

	// Style tables
	ts := table.DefaultStyles()
	ts.Header = ts.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true)
	ts.Selected = ts.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(true)

	epTable.SetStyles(ts)
	patTable.SetStyles(ts)
	conTable.SetStyles(ts)
	graphTable.SetStyles(ts)

	si := textinput.New()
	si.Placeholder = "Type a search query..."
	si.Width = 50

	ri := textinput.New()
	ri.Placeholder = "Filter by repo (optional)..."
	ri.Width = 40

	pi := textarea.New()
	pi.Placeholder = "Paste a raw prompt to polish (Press ctrl+p or ctrl+s to run)..."
	pi.SetWidth(40)
	pi.SetHeight(10)

	dvp := viewport.New(80, 20)
	dvp.Style = lipgloss.NewStyle().Padding(0, 1)

	pvp := viewport.New(40, 10)
	pvp.Style = lipgloss.NewStyle().Padding(0, 1)
	pvp.SetContent("Press ctrl+p or ctrl+s to polish prompt.\n")

	return model{
		es:      es,
		cfgPath: cfgPath,
		cfg:     cfg,
		tabNames: []string{
			"Episodes", "Patterns", "Search",
			"Consolidation", "Polish", "Stats",
			"Concepts", "Graph",
		},
		epTable:     epTable,
		patTable:    patTable,
		conTable:    conTable,
		graphTable:  graphTable,
		searchInput: si,
		repoInput:   ri,
		polishInput: pi,
		detailVP:    dvp,
		polishVP:    pvp,
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
			Edit:     key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("^O", "edit in $EDITOR")),
			Promote:  key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "promote to concept")),
			Traverse: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "traverse graph")),
			Polish:   key.NewBinding(key.WithKeys("ctrl+p", "ctrl+s"), key.WithHelp("ctrl+p/s", "polish")),
		},
		consolidationMsg: "Press [c] to find merge candidates",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.loadEpisodes(),
		m.loadPatterns(),
		m.loadConcepts(),
		m.loadEdges(),
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

func (m model) loadConcepts() tea.Cmd {
	return func() tea.Msg {
		cons, err := m.es.ListConcepts(100, 0, "")
		if err != nil {
			return loadConceptsMsg{nil, err}
		}
		return loadConceptsMsg{cons, nil}
	}
}

func (m model) loadEdges() tea.Cmd {
	return func() tea.Msg {
		edges, err := m.es.ListEdges("")
		if err != nil {
			return loadEdgesMsg{nil, err}
		}
		return loadEdgesMsg{edges, nil}
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

type loadConceptsMsg struct {
	concepts []store.SemanticConcept
	err      error
}

type loadEdgesMsg struct {
	edges []store.GraphEdge
	err   error
}

type errorMsg string

type polishResultMsg struct {
	result *prompter.PolishResult
}

func (m *model) getPolishBoxHeight() int {
	statsLines := 4
	if m.polishResult != nil {
		statsLines = 5
	}
	maxHistory := 3
	if m.height < 16 {
		maxHistory = 1
	}
	if m.height < 13 {
		maxHistory = 0
	}
	historyLines := 0
	if len(m.polishHistory) > 0 && maxHistory > 0 {
		hLines := len(m.polishHistory)
		if hLines > maxHistory {
			hLines = maxHistory
		}
		historyLines = 3 + hLines
	}
	errLines := 0
	if m.errMsg != "" {
		errLines = 2
	}
	nonPaneLines := 6 + statsLines + historyLines + errLines
	boxHeight := m.height - nonPaneLines
	if boxHeight < 4 {
		boxHeight = 4
	}
	return boxHeight
}

func (m *model) recalcSizing() {
	leftWidth := int(float64(m.width) * 0.58)
	rightWidth := m.width - leftWidth - 1
	if leftWidth < 25 {
		leftWidth = 25
	}
	if rightWidth < 15 {
		rightWidth = 15
	}
	if leftWidth+rightWidth+1 > m.width {
		leftWidth = m.width * 58 / 100
		if leftWidth < 15 {
			leftWidth = 15
		}
		rightWidth = m.width - leftWidth - 1
		if rightWidth < 10 {
			rightWidth = 10
		}
	}

	errLines := 0
	if m.errMsg != "" {
		errLines = 2
	}
	paneHeight := m.height - 7 - errLines
	if paneHeight < 5 {
		paneHeight = 5
	}

	m.detailVP.Width = rightWidth - 4
	if m.detailVP.Width < 10 {
		m.detailVP.Width = 10
	}
	m.detailVP.Height = paneHeight - 3

	m.epTable.SetWidth(leftWidth)
	m.patTable.SetWidth(leftWidth)
	m.conTable.SetWidth(leftWidth)
	m.graphTable.SetWidth(leftWidth)

	if m.detailRaw != "" {
		wrapped := lipgloss.NewStyle().Width(m.detailVP.Width).Render(m.detailRaw)
		m.detailVP.SetContent(wrapped)
	}

	halfWidth := (m.width - 6) / 2
	if halfWidth < 15 {
		halfWidth = 15
	}
	boxHeight := m.getPolishBoxHeight()

	m.polishInput.SetWidth(halfWidth - 4)
	if m.polishInput.Width() < 10 {
		m.polishInput.SetWidth(10)
	}
	m.polishInput.SetHeight(boxHeight - 4)

	m.polishVP.Width = halfWidth - 4
	if m.polishVP.Width < 10 {
		m.polishVP.Width = 10
	}
	m.polishVP.Height = boxHeight - 4

	if m.polishResult != nil {
		wrapped := lipgloss.NewStyle().Width(m.polishVP.Width).Render(m.polishResult.PolishedPrompt)
		m.polishVP.SetContent(wrapped)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.recalcSizing()

	case tea.KeyMsg:
		m.errMsg = ""

		// 1. If an input is focused, let it process the key and return immediately
		if m.activeTab == 2 && (m.searchInput.Focused() || m.repoInput.Focused()) {
			if msg.String() != "tab" && msg.String() != "shift+tab" && !key.Matches(msg, m.keys.Quit) {
				switch msg.String() {
				case "esc":
					m.searchInput.Blur()
					m.repoInput.Blur()
					return m, nil
				case "enter":
					return m, m.runSearch()
				default:
					var cmd tea.Cmd
					if m.searchInput.Focused() {
						m.searchInput, cmd = m.searchInput.Update(msg)
					} else {
						m.repoInput, cmd = m.repoInput.Update(msg)
					}
					return m, cmd
				}
			}
		}

		if m.activeTab == 4 && m.polishInput.Focused() {
			if msg.String() != "tab" && msg.String() != "shift+tab" && !key.Matches(msg, m.keys.Quit) {
				switch msg.String() {
				case "esc":
					m.polishInput.Blur()
					return m, nil
				case "ctrl+p", "ctrl+s":
					return m, m.runPolish()
				case "ctrl+o":
					return m, m.editInEditor()
				default:
					var cmd tea.Cmd
					m.polishInput, cmd = m.polishInput.Update(msg)
					return m, cmd
				}
			}
		}

		// 2. If details are focused, let details scroll
		if m.detailFocused {
			switch {
			case key.Matches(msg, m.keys.Back):
				m.detailFocused = false
				return m, nil
			case key.Matches(msg, m.keys.Quit):
				return m, tea.Quit
			default:
				var cmd tea.Cmd
				m.detailVP, cmd = m.detailVP.Update(msg)
				return m, cmd
			}
		}

		// 3. Global hotkeys
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
		case key.Matches(msg, m.keys.Tab):
			m.blurActiveInput()
			m.detailFocused = false
			m.activeTab = (m.activeTab + 1) % len(m.tabNames)
			cmds = append(cmds, m.focusActiveInput()...)
			if m.activeTab == 3 {
				m.refreshConsolidation()
			}
			if m.activeTab == 5 {
				cmds = append(cmds, m.loadStats())
			}
			if m.activeTab == 6 {
				cmds = append(cmds, m.loadConcepts())
			}
			if m.activeTab == 7 {
				cmds = append(cmds, m.loadConcepts(), m.loadEdges())
			}
		case key.Matches(msg, m.keys.ShiftTab):
			m.blurActiveInput()
			m.detailFocused = false
			m.activeTab = (m.activeTab - 1 + len(m.tabNames)) % len(m.tabNames)
			cmds = append(cmds, m.focusActiveInput()...)
			if m.activeTab == 3 {
				m.refreshConsolidation()
			}
			if m.activeTab == 5 {
				cmds = append(cmds, m.loadStats())
			}
			if m.activeTab == 6 {
				cmds = append(cmds, m.loadConcepts())
			}
			if m.activeTab == 7 {
				cmds = append(cmds, m.loadConcepts(), m.loadEdges())
			}
		}

		if m.activeTab == 0 {
			switch {
			case key.Matches(msg, m.keys.Enter):
				if len(m.episodes) > 0 {
					m.detailFocused = true
					return m, nil
				}
			case key.Matches(msg, m.keys.Delete):
				if len(m.episodes) > 0 && m.epTable.Cursor() >= 0 && m.epTable.Cursor() < len(m.episodes) {
					ep := m.episodes[m.epTable.Cursor()]
					return m, m.deleteEpisode(ep.ID)
				}
			case key.Matches(msg, m.keys.Promote):
				if len(m.episodes) > 0 && m.epTable.Cursor() >= 0 && m.epTable.Cursor() < len(m.episodes) {
					ep := m.episodes[m.epTable.Cursor()]
					return m, m.promoteEpisode(ep.ID)
				}
			case key.Matches(msg, m.keys.Traverse):
				if len(m.episodes) > 0 && m.epTable.Cursor() >= 0 && m.epTable.Cursor() < len(m.episodes) {
					ep := m.episodes[m.epTable.Cursor()]
					return m, m.runTraverse(ep.ID)
				}
			default:
				var cmd tea.Cmd
				oldCursor := m.epTable.Cursor()
				m.epTable, cmd = m.epTable.Update(msg)
				if m.epTable.Cursor() != oldCursor && len(m.episodes) > 0 && m.epTable.Cursor() < len(m.episodes) {
					ep := m.episodes[m.epTable.Cursor()]
					return m, tea.Batch(cmd, m.loadEpisodeDetail(ep.ID))
				}
				return m, cmd
			}
		}

		if m.activeTab == 1 {
			switch {
			case key.Matches(msg, m.keys.Enter):
				if len(m.patterns) > 0 {
					m.detailFocused = true
					return m, nil
				}
			case key.Matches(msg, m.keys.Delete):
				if len(m.patterns) > 0 && m.patTable.Cursor() >= 0 && m.patTable.Cursor() < len(m.patterns) {
					return m, m.deletePattern(m.patterns[m.patTable.Cursor()].ID)
				}
			default:
				var cmd tea.Cmd
				oldCursor := m.patTable.Cursor()
				m.patTable, cmd = m.patTable.Update(msg)
				if m.patTable.Cursor() != oldCursor && len(m.patterns) > 0 && m.patTable.Cursor() < len(m.patterns) {
					pat := m.patterns[m.patTable.Cursor()]
					return m, tea.Batch(cmd, m.loadPatternDetail(pat.ID))
				}
				return m, cmd
			}
		}

		if m.activeTab == 2 {
			if msg.Type == tea.KeyEnter && (m.searchInput.Focused() || m.repoInput.Focused()) {
				return m, m.runSearch()
			}
			if msg.Type == tea.KeyEnter && !m.searchInput.Focused() && !m.repoInput.Focused() && len(m.searchResults) > 0 {
				return m, m.loadEpisodeDetail(m.searchResults[0].ID)
			}
			if msg.String() == "ctrl+f" {
				if m.searchInput.Focused() {
					m.searchInput.Blur()
					cmds = append(cmds, m.repoInput.Focus())
				} else {
					m.repoInput.Blur()
					cmds = append(cmds, m.searchInput.Focus())
				}
			}
			if key.Matches(msg, m.keys.Back) {
				m.searchResults = nil
			}
		}

		if m.activeTab == 4 {
			switch {
			case key.Matches(msg, m.keys.Polish):
				return m, m.runPolish()
			case key.Matches(msg, m.keys.Back):
				m.polishResult = nil
				m.polishVP.SetContent("Press ctrl+p or ctrl+s to polish prompt.\n")
			case key.Matches(msg, m.keys.Edit):
				return m, m.editInEditor()
			default:
				if !m.polishInput.Focused() {
					var cmd tea.Cmd
					m.polishVP, cmd = m.polishVP.Update(msg)
					return m, cmd
				}
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

		if m.activeTab == 6 {
			switch {
			case key.Matches(msg, m.keys.Enter):
				if len(m.concepts) > 0 {
					m.detailFocused = true
					return m, nil
				}
			case key.Matches(msg, m.keys.Delete):
				if len(m.concepts) > 0 && m.conTable.Cursor() >= 0 && m.conTable.Cursor() < len(m.concepts) {
					con := m.concepts[m.conTable.Cursor()]
					return m, m.deleteConcept(con.ID)
				}
			case key.Matches(msg, m.keys.Traverse):
				if len(m.concepts) > 0 && m.conTable.Cursor() >= 0 && m.conTable.Cursor() < len(m.concepts) {
					con := m.concepts[m.conTable.Cursor()]
					return m, m.runTraverse(con.ID)
				}
			default:
				var cmd tea.Cmd
				oldCursor := m.conTable.Cursor()
				m.conTable, cmd = m.conTable.Update(msg)
				if m.conTable.Cursor() != oldCursor && len(m.concepts) > 0 && m.conTable.Cursor() < len(m.concepts) {
					con := m.concepts[m.conTable.Cursor()]
					return m, tea.Batch(cmd, m.loadConceptDetail(con.ID))
				}
				return m, cmd
			}
		}

		if m.activeTab == 7 {
			switch {
			case key.Matches(msg, m.keys.Enter) || key.Matches(msg, m.keys.Traverse):
				if len(m.concepts) > 0 && m.conTable.Cursor() >= 0 && m.conTable.Cursor() < len(m.concepts) {
					con := m.concepts[m.conTable.Cursor()]
					return m, m.runTraverse(con.ID)
				}
			default:
				var cmd tea.Cmd
				oldCursor := m.conTable.Cursor()
				m.conTable, cmd = m.conTable.Update(msg)
				if m.conTable.Cursor() != oldCursor && len(m.concepts) > 0 && m.conTable.Cursor() < len(m.concepts) {
					con := m.concepts[m.conTable.Cursor()]
					return m, tea.Batch(cmd, m.loadConceptDetail(con.ID))
				}
				return m, cmd
			}
		}

	case loadEpisodesMsg:
		if msg.err == nil {
			m.episodes = msg.episodes
			m.refreshEpTable()
			if len(m.episodes) > 0 && m.epTable.Cursor() >= 0 && m.epTable.Cursor() < len(m.episodes) {
				cmds = append(cmds, m.loadEpisodeDetail(m.episodes[m.epTable.Cursor()].ID))
			}
		}
	case loadPatternsMsg:
		if msg.err == nil {
			m.patterns = msg.patterns
			m.refreshPatTable()
			if len(m.patterns) > 0 && m.patTable.Cursor() >= 0 && m.patTable.Cursor() < len(m.patterns) {
				cmds = append(cmds, m.loadPatternDetail(m.patterns[m.patTable.Cursor()].ID))
			}
		}
	case loadConceptsMsg:
		if msg.err == nil {
			m.concepts = msg.concepts
			m.refreshConTable()
			if len(m.concepts) > 0 && m.conTable.Cursor() >= 0 && m.conTable.Cursor() < len(m.concepts) {
				cmds = append(cmds, m.loadConceptDetail(m.concepts[m.conTable.Cursor()].ID))
			}
		}
	case loadEdgesMsg:
		if msg.err == nil {
			m.graphEdges = msg.edges
			m.refreshGraphTable()
		}
	case episodeDetailMsg:
		if msg.err == nil {
			m.detailRaw = formatEpisode(msg.ep, msg.related, msg.edges)
			wrapped := lipgloss.NewStyle().Width(m.detailVP.Width).Render(m.detailRaw)
			m.detailVP.SetContent(wrapped)
			m.detailVP.GotoTop()
		}
	case patternDetailMsg:
		if msg.err == nil {
			m.detailRaw = formatPattern(msg.pat)
			wrapped := lipgloss.NewStyle().Width(m.detailVP.Width).Render(m.detailRaw)
			m.detailVP.SetContent(wrapped)
			m.detailVP.GotoTop()
		}
	case conceptDetailMsg:
		if msg.err == nil {
			m.detailRaw = formatConcept(msg.con, msg.edges)
			wrapped := lipgloss.NewStyle().Width(m.detailVP.Width).Render(m.detailRaw)
			m.detailVP.SetContent(wrapped)
			m.detailVP.GotoTop()
		}
	case graphTraverseMsg:
		if msg.err == nil {
			m.detailFocused = true
			var b strings.Builder
			fmt.Fprintf(&b, "Traversal Path Starting From: %s\n", msg.startID)
			fmt.Fprintf(&b, "%s\n", strings.Repeat("─", m.width-2))
			if len(msg.results) == 0 {
				b.WriteString("No connected nodes found in traversal.\n")
			} else {
				for i, r := range msg.results {
					fmt.Fprintf(&b, "%d. Target: %s [%s/%s] (weight/score: %.2f)\n", i+1, r.ID, r.Domain, r.Outcome, r.LocalScore)
					fmt.Fprintf(&b, "   Problem: %s\n\n", truncate(r.Problem, 80))
				}
			}
			m.detailRaw = b.String()
			wrapped := lipgloss.NewStyle().Width(m.detailVP.Width).Render(m.detailRaw)
			m.detailVP.SetContent(wrapped)
			m.detailVP.GotoTop()
		} else {
			m.errMsg = fmt.Sprintf("Traverse failed: %v", msg.err)
		}
	case deleteMsg:
		if msg.err == nil {
			return m, tea.Batch(m.loadEpisodes(), m.loadPatterns(), m.loadConcepts(), m.loadEdges())
		}
		m.errMsg = fmt.Sprintf("Action failed: %v", msg.err)
	case polishResultMsg:
		if msg.result == nil {
			m.errMsg = "Polish failed"
			break
		}
		m.polishResult = msg.result
		m.recalcSizing()
		wrapped := lipgloss.NewStyle().Width(m.polishVP.Width).Render(msg.result.PolishedPrompt)
		m.polishVP.SetContent(wrapped)
		m.polishVP.GotoTop()

		m.polishHistory = append(m.polishHistory, polishEntry{
			original: m.polishInput.Value(),
			result:   msg.result,
		})
		m.polishInput.SetValue("")
		m.polishInput.Blur()
		m.recalcSizing()

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
		m.recalcSizing()
	}

	if m.activeTab == 2 {
		if m.searchInput.Focused() {
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			cmds = append(cmds, cmd)
		}
		if m.repoInput.Focused() {
			var cmd tea.Cmd
			m.repoInput, cmd = m.repoInput.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	if m.activeTab == 4 && m.polishInput.Focused() {
		var cmd tea.Cmd
		m.polishInput, cmd = m.polishInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	m.recalcSizing()
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

		// Color-code outcomes using Lipgloss
		outcome := ep.Outcome
		switch outcome {
		case "success":
			outcome = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true).Render("success")
		case "failure":
			outcome = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true).Render("failure")
		default:
			outcome = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true).Render(outcome)
		}

		rows = append(rows, table.Row{
			ep.ID, ep.Domain, outcome, tags, dur,
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

func (m *model) refreshConTable() {
	var rows []table.Row
	for _, con := range m.concepts {
		desc := con.Description
		if len(desc) > 33 {
			desc = desc[:33] + ".."
		}
		rows = append(rows, table.Row{
			con.ID, con.EntityName, con.Type, desc, fmt.Sprintf("%d", con.AccessCount),
		})
	}
	m.conTable.SetRows(rows)
}

func (m *model) refreshGraphTable() {
	var rows []table.Row
	for _, edge := range m.graphEdges {
		rows = append(rows, table.Row{
			edge.SourceID, edge.TargetID, edge.Relationship, fmt.Sprintf("%.2f", edge.Weight),
		})
	}
	m.graphTable.SetRows(rows)
}

func (m *model) blurActiveInput() {
	switch m.activeTab {
	case 2:
		m.searchInput.Blur()
		m.repoInput.Blur()
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
			return episodeDetailMsg{err: err}
		}
		related, _ := m.es.GetRelatedEpisodes(id)
		edges, _ := m.es.ListEdges(id)
		return episodeDetailMsg{ep: ep, related: related, edges: edges}
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

func (m model) loadConceptDetail(id string) tea.Cmd {
	return func() tea.Msg {
		con, err := m.es.GetConcept(id)
		if err != nil {
			return conceptDetailMsg{err: err}
		}
		edges, _ := m.es.ListEdges(id)
		return conceptDetailMsg{con: con, edges: edges}
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

func (m model) deleteConcept(id string) tea.Cmd {
	return func() tea.Msg {
		return deleteMsg{m.es.DeleteConcept(id)}
	}
}

func (m model) promoteEpisode(id string) tea.Cmd {
	return func() tea.Msg {
		conceptID, err := m.es.PromoteConceptFromEpisode(id)
		if err != nil {
			return errorMsg(fmt.Sprintf("Promote failed: %v", err))
		}
		_, _ = m.es.AddEdge(id, conceptID, "promoted_to", 1.0)
		return deleteMsg{nil}
	}
}

func (m model) runTraverse(startID string) tea.Cmd {
	return func() tea.Msg {
		results, err := m.es.Traverse(startID, "", 3)
		return graphTraverseMsg{results: results, startID: startID, err: err}
	}
}

type episodeDetailMsg struct {
	ep      *models.Episode
	related []string
	edges   []store.GraphEdge
	err     error
}

type patternDetailMsg struct {
	pat *models.Pattern
	err error
}

type conceptDetailMsg struct {
	con   *store.SemanticConcept
	edges []store.GraphEdge
	err   error
}

type graphTraverseMsg struct {
	results []models.EpisodeSummary
	startID string
	err     error
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
			if _, err := f.WriteString(m.polishInput.Value()); err != nil {
				return editContentMsg{"", err}
			}
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
		repo := m.repoInput.Value()
		if q == "" && repo == "" {
			return searchResultsMsg{nil, nil}
		}
		results, err := m.es.SearchLocal(q, "", "", repo, nil, 20)
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

func (m model) loadStats() tea.Cmd {
	return func() tea.Msg {
		epTotal, _ := m.es.EpisodeCount()
		patTotal, _ := m.es.PatternCount()
		byDomain, _ := m.es.EpisodesByDomain()
		byOutcome, _ := m.es.EpisodesByOutcome()
		byRepo, _ := m.es.EpisodesByRepo()
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
			EpisodesByRepo:        byRepo,
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
			sr.TopRepo = summary.TopRepo
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

func formatEpisode(ep *models.Episode, related []string, edges []store.GraphEdge) string {
	var b strings.Builder
	lbl := func(s string) string {
		return lipgloss.NewStyle().Foreground(ghHighlight).Render(s)
	}
	val := func(s string) string {
		return lipgloss.NewStyle().Foreground(ghBright).Render(s)
	}
	dim := func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9")).Render(s)
	}

	fmt.Fprintln(&b, lbl("Problem:"))
	fmt.Fprintln(&b, val(ep.Problem))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lbl("Domain:"))
	fmt.Fprintln(&b, dim(fmt.Sprintf("%s | Duration: %ds", ep.Domain, ep.DurationSeconds)))
	if len(ep.Tags) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, lbl("Tags:"))
		fmt.Fprintln(&b, dim(strings.Join(ep.Tags, ", ")))
	}
	if ep.Repo != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, lbl("Repo:"))
		fmt.Fprintln(&b, dim(ep.Repo))
	}
	if ep.ThinkingTrace != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, lbl("Thinking Steps:"))
		for i, line := range strings.Split(ep.ThinkingTrace, "\n") {
			if line == "" {
				continue
			}
			fmt.Fprintf(&b, "%s\n", dim(fmt.Sprintf("%d. %s", i+1, line)))
		}
	}
	if len(related) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, lbl("Related:"))
		fmt.Fprintln(&b, dim(strings.Join(related, ", ")))
	}
	if len(edges) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, lbl("Graph Edges:"))
		for _, e := range edges {
			fmt.Fprintf(&b, "  • %s\n", dim(fmt.Sprintf("%s —(%s, w=%.2f)→ %s", e.SourceID, e.Relationship, e.Weight, e.TargetID)))
		}
	}
	if len(ep.ToolCalls) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "%s (%d):\n", lbl("Tool Calls"), len(ep.ToolCalls))
		for _, tc := range ep.ToolCalls {
			fmt.Fprintf(&b, "  • %s\n", dim(fmt.Sprintf("%s → %s", tc.Tool, tc.Outcome)))
		}
	}
	return b.String()
}

func formatPattern(pat *models.Pattern) string {
	var b strings.Builder
	lbl := func(s string) string {
		return lipgloss.NewStyle().Foreground(ghPurple).Render(s)
	}
	dim := func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#c9d1d9")).Render(s)
	}

	fmt.Fprintln(&b, lbl("Consolidated Prompt:"))
	fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(ghBright).Render(pat.ConsolidatedPrompt))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lbl("Master Thinking Path:"))
	fmt.Fprintln(&b, dim(pat.MasterThinkingPath))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lbl("Sources:"))
	fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(ghSubtle).Render(strings.Join(pat.Sources, ", ")))
	return b.String()
}

func formatConcept(con *store.SemanticConcept, edges []store.GraphEdge) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Semantic Concept: %s\n\n", con.ID)
	fmt.Fprintf(&b, "Entity Name: %s  |  Type: %s\n", con.EntityName, con.Type)
	fmt.Fprintf(&b, "Access Count: %d  |  Created: %s\n", con.AccessCount, con.CreatedAt)
	if con.LastAccessedAt != "" {
		fmt.Fprintf(&b, "Last Accessed: %s\n", con.LastAccessedAt)
	}
	if con.SourceEpisode != "" {
		fmt.Fprintf(&b, "Source Episode: %s\n", con.SourceEpisode)
	}
	if len(con.Tags) > 0 {
		fmt.Fprintf(&b, "Tags: %s\n", strings.Join(con.Tags, ", "))
	}
	if len(edges) > 0 {
		fmt.Fprintf(&b, "\nRelationships:\n")
		for _, e := range edges {
			fmt.Fprintf(&b, "  • %s —(%s, weight=%.2f)→ %s\n", e.SourceID, e.Relationship, e.Weight, e.TargetID)
		}
	}
	fmt.Fprintf(&b, "\nDescription:\n%s\n", con.Description)
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

	names := m.tabNames
	if m.width < 95 {
		names = []string{"Eps", "Pats", "Find", "Cons", "Polish", "Stats", "Cpts", "Graph"}
	}

	padding := 1
	if m.width < 60 {
		padding = 0
	}

	tabStyleLocal := lipgloss.NewStyle().Padding(0, padding).Foreground(ghSubtle)
	activeTabLocal := lipgloss.NewStyle().Padding(0, padding).
		Foreground(ghBright).
		Background(ghDarkBg).
		Bold(true)

	tabs := make([]string, len(names))
	for i, name := range names {
		if i == m.activeTab {
			tabs[i] = activeTabLocal.Render(name)
		} else {
			tabs[i] = tabStyleLocal.Render(name)
		}
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	b.WriteString("\n")

	dividerWidth := m.width - 1
	if dividerWidth < 10 {
		dividerWidth = 10
	}
	divider := lipgloss.NewStyle().Foreground(ghBorder).Render(strings.Repeat("─", dividerWidth))
	b.WriteString(divider)
	b.WriteString("\n")

	if m.height < 9 {
		b.WriteString(lipgloss.NewStyle().Foreground(ghSubtle).Render("Terminal height too small; resize to show dashboard panes."))
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(ghSubtle).Render("q: quit | tab: next tab"))
		return b.String()
	}

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
	case 6:
		b.WriteString(m.conceptsView())
	case 7:
		b.WriteString(m.graphView())
	}

	if m.errMsg != "" {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(ghRed).Render("  ✗ "+m.errMsg))
	}
	b.WriteString("\n")
	// Footer matching visualization.html style
	footerText := "q: quit | tab: next tab | enter: select | d: delete | p: promote | ?: help"
	if m.width < 76 {
		footerText = "q: quit | tab: next | enter: sel | d: del | p: promo | ?: help"
	}
	if m.width < 60 {
		footerText = "q: quit | tab: next | enter: sel | d: del"
	}
	if m.width < 45 {
		footerText = "q: quit | tab: next | enter: sel"
	}
	footer := lipgloss.NewStyle().Foreground(ghSubtle).Render(footerText)
	b.WriteString(footer)

	return b.String()
}

func (m model) detailView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Detail View") + "\n")
	b.WriteString(lipgloss.NewStyle().Foreground(ghSubtle).Render("Press esc to go back, q to quit") + "\n")

	dividerWidth := m.width - 1
	if dividerWidth < 10 {
		dividerWidth = 10
	}
	divider := lipgloss.NewStyle().Foreground(ghBorder).Render(strings.Repeat("─", dividerWidth))
	b.WriteString(divider + "\n")

	content := m.detailVP.View()
	b.WriteString(content)

	return b.String()
}

func (m model) renderEpisodesList(width, height int) string {
	if len(m.episodes) == 0 {
		return "  No episodes found\n"
	}
	var b strings.Builder
	cursor := m.epTable.Cursor()

	start := 0
	end := len(m.episodes)
	if len(m.episodes) > height {
		start = cursor - height/2
		if start < 0 {
			start = 0
		}
		end = start + height
		if end > len(m.episodes) {
			end = len(m.episodes)
			start = end - height
		}
	}

	interiorWidth := width - 2
	if interiorWidth < 20 {
		interiorWidth = 20
	}

	for i := start; i < end; i++ {
		ep := m.episodes[i]
		badge := ""
		if ep.Outcome == "success" {
			if interiorWidth < 50 {
				badge = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(ghGreen).Bold(true).Render(" S ")
			} else {
				badge = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(ghGreen).Bold(true).Render(" success ")
			}
		} else {
			if interiorWidth < 50 {
				badge = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(ghRed).Bold(true).Render(" F ")
			} else {
				badge = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(ghRed).Bold(true).Render(" failure ")
			}
		}

		idW := 15
		if interiorWidth < 60 {
			idW = 8
		}
		if interiorWidth < 30 {
			idW = 5
		}
		idVal := truncate(ep.ID, idW)

		var line string
		if interiorWidth < 45 {
			usedWidth := 8 + idW + 3
			probW := interiorWidth - usedWidth
			if probW < 5 {
				probW = 5
			}
			prob := truncate(ep.Problem, probW)

			if i == cursor {
				idStr := lipgloss.NewStyle().Foreground(ghHighlight).Bold(true).Render(idVal)
				line = fmt.Sprintf("▶ %s | %s | %s", idStr, badge, prob)
			} else {
				line = fmt.Sprintf("  %s | %s | %s", idVal, badge, prob)
			}
		} else {
			domW := 8
			if interiorWidth < 65 {
				domW = 5
			}
			domVal := truncate(ep.Domain, domW)

			badgeLen := 9
			if interiorWidth < 50 {
				badgeLen = 3
			}
			usedWidth := 11 + idW + badgeLen + domW
			probW := interiorWidth - usedWidth
			if probW < 5 {
				probW = 5
			}
			prob := truncate(ep.Problem, probW)

			if i == cursor {
				idStr := lipgloss.NewStyle().Foreground(ghHighlight).Bold(true).Render(idVal)
				line = fmt.Sprintf("▶ %s | %s | %s | %s", idStr, badge, domVal, prob)
			} else {
				line = fmt.Sprintf("  %s | %s | %s | %s", idVal, badge, domVal, prob)
			}
		}

		fmt.Fprintln(&b, renderScrollableListLine(line, i == cursor, i-start, start, len(m.episodes), height, interiorWidth))
	}
	return b.String()
}

func (m model) renderPatternsList(width, height int) string {
	if len(m.patterns) == 0 {
		return "  No patterns found\n"
	}
	var b strings.Builder
	cursor := m.patTable.Cursor()

	start := 0
	end := len(m.patterns)
	if len(m.patterns) > height {
		start = cursor - height/2
		if start < 0 {
			start = 0
		}
		end = start + height
		if end > len(m.patterns) {
			end = len(m.patterns)
			start = end - height
		}
	}

	interiorWidth := width - 2
	if interiorWidth < 20 {
		interiorWidth = 20
	}

	for i := start; i < end; i++ {
		pat := m.patterns[i]
		scoreStr := fmt.Sprintf("score: %.3f", pat.MergeScore)
		sourcesStr := fmt.Sprintf("%d sources", len(pat.Sources))

		idW := 18
		domW := 8
		if interiorWidth < 60 {
			idW = 10
			domW = 5
			scoreStr = fmt.Sprintf("s:%.2f", pat.MergeScore)
			sourcesStr = fmt.Sprintf("%ds", len(pat.Sources))
		}

		idVal := truncate(pat.ID, idW)
		domVal := truncate(pat.Domain, domW)

		var line string
		if interiorWidth < 45 {
			usedW := 8 + len(idVal) + len(scoreStr) + len(sourcesStr)
			if usedW > interiorWidth {
				sourcesStr = fmt.Sprintf("%d", len(pat.Sources))
			}
			if i == cursor {
				idStr := lipgloss.NewStyle().Foreground(ghHighlight).Bold(true).Render(idVal)
				line = fmt.Sprintf("▶ %s | %s | %s", idStr, scoreStr, sourcesStr)
			} else {
				line = fmt.Sprintf("  %s | %s | %s", idVal, scoreStr, sourcesStr)
			}
		} else {
			if i == cursor {
				idStr := lipgloss.NewStyle().Foreground(ghHighlight).Bold(true).Render(idVal)
				line = fmt.Sprintf("▶ %s | %s | %s | %s", idStr, domVal, scoreStr, sourcesStr)
			} else {
				line = fmt.Sprintf("  %s | %s | %s | %s", idVal, domVal, scoreStr, sourcesStr)
			}
		}

		fmt.Fprintln(&b, renderScrollableListLine(line, i == cursor, i-start, start, len(m.patterns), height, interiorWidth))
	}
	return b.String()
}

func (m model) renderConceptsList(width, height int) string {
	if len(m.concepts) == 0 {
		return "  No semantic concepts found\n"
	}
	var b strings.Builder
	cursor := m.conTable.Cursor()

	start := 0
	end := len(m.concepts)
	if len(m.concepts) > height {
		start = cursor - height/2
		if start < 0 {
			start = 0
		}
		end = start + height
		if end > len(m.concepts) {
			end = len(m.concepts)
			start = end - height
		}
	}

	interiorWidth := width - 2
	if interiorWidth < 20 {
		interiorWidth = 20
	}

	for i := start; i < end; i++ {
		con := m.concepts[i]
		accessStr := fmt.Sprintf("%d accesses", con.AccessCount)
		if interiorWidth < 50 {
			accessStr = fmt.Sprintf("%da", con.AccessCount)
		}

		avail := interiorWidth - 11 - len(accessStr)
		if avail < 8 {
			avail = 8
		}
		idW := int(float64(avail) * 0.4)
		entityW := int(float64(avail) * 0.3)
		typeW := avail - idW - entityW
		if idW < 4 {
			idW = 4
		}
		if entityW < 4 {
			entityW = 4
		}
		if typeW < 4 {
			typeW = 4
		}

		idVal := truncate(con.ID, idW)
		entityVal := truncate(con.EntityName, entityW)
		typeVal := truncate(con.Type, typeW)

		var line string
		if interiorWidth < 45 {
			availLocal := interiorWidth - 8 - len(accessStr)
			if availLocal < 6 {
				availLocal = 6
			}
			idWLocal := int(float64(availLocal) * 0.5)
			entityWLocal := availLocal - idWLocal

			idValLocal := truncate(con.ID, idWLocal)
			entityValLocal := truncate(con.EntityName, entityWLocal)

			if i == cursor {
				idStr := lipgloss.NewStyle().Foreground(ghHighlight).Bold(true).Render(idValLocal)
				line = fmt.Sprintf("▶ %s | %s | %s", idStr, entityValLocal, accessStr)
			} else {
				line = fmt.Sprintf("  %s | %s | %s", idValLocal, entityValLocal, accessStr)
			}
		} else {
			if i == cursor {
				idStr := lipgloss.NewStyle().Foreground(ghHighlight).Bold(true).Render(idVal)
				line = fmt.Sprintf("▶ %s | %s | %s | %s", idStr, entityVal, typeVal, accessStr)
			} else {
				line = fmt.Sprintf("  %s | %s | %s | %s", idVal, entityVal, typeVal, accessStr)
			}
		}

		fmt.Fprintln(&b, renderScrollableListLine(line, i == cursor, i-start, start, len(m.concepts), height, interiorWidth))
	}
	return b.String()
}

// dashedBorder matches visualization.html `border: 1px dashed #30363d`
var dashedBorder = lipgloss.Border{
	Top:         "╌",
	Bottom:      "╌",
	Left:        "┆",
	Right:       "┆",
	TopLeft:     "┌",
	TopRight:    "┐",
	BottomLeft:  "└",
	BottomRight: "┘",
}

func (m model) drawSplitPane(leftHeader, leftContent, rightHeader, rightContent string) string {
	leftWidth := int(float64(m.width) * 0.58)
	rightWidth := m.width - leftWidth - 1
	if leftWidth < 25 {
		leftWidth = 25
	}
	if rightWidth < 15 {
		rightWidth = 15
	}
	if leftWidth+rightWidth+1 > m.width {
		leftWidth = m.width * 58 / 100
		if leftWidth < 15 {
			leftWidth = 15
		}
		rightWidth = m.width - leftWidth - 1
		if rightWidth < 10 {
			rightWidth = 10
		}
	}

	errLines := 0
	if m.errMsg != "" {
		errLines = 2
	}
	paneHeight := m.height - 7 - errLines
	if paneHeight < 5 {
		paneHeight = 5
	}

	leftBorderColor := ghBorder
	rightBorderColor := ghBorder
	if m.detailFocused {
		rightBorderColor = ghHighlight
	} else {
		leftBorderColor = ghHighlight
	}

	leftStyle := lipgloss.NewStyle().
		Border(dashedBorder).
		BorderForeground(leftBorderColor).
		Width(leftWidth - 2).
		Height(paneHeight - 2)

	rightStyle := lipgloss.NewStyle().
		Border(dashedBorder).
		BorderForeground(rightBorderColor).
		Width(rightWidth - 2).
		Height(paneHeight - 2)

	leftHeaderTrunc := truncate(leftHeader, leftWidth-4)
	var leftBody strings.Builder
	leftBody.WriteString(lipgloss.NewStyle().Foreground(ghSubtle).Render(" "+leftHeaderTrunc+":") + "\n")
	leftBody.WriteString(leftContent)

	rightHeaderTrunc := truncate(rightHeader, rightWidth-4)
	var rightBody strings.Builder
	rightBody.WriteString(lipgloss.NewStyle().Foreground(ghSubtle).Render(" "+rightHeaderTrunc+":") + "\n")
	rightBody.WriteString(rightContent)

	return lipgloss.JoinHorizontal(lipgloss.Top,
		leftStyle.Render(leftBody.String()),
		" ",
		rightStyle.Render(rightBody.String()),
	)
}

func (m model) episodesView() string {
	leftWidth := int(float64(m.width) * 0.58)
	rightWidth := m.width - leftWidth - 1
	errLines := 0
	if m.errMsg != "" {
		errLines = 2
	}
	paneHeight := m.height - 7 - errLines
	if paneHeight < 5 {
		paneHeight = 5
	}

	leftContent := m.renderEpisodesList(leftWidth, paneHeight-3)

	detailContent := renderViewportWithScrollbar(m.detailVP, len(strings.Split(m.detailRaw, "\n")))
	if detailContent == "" {
		detailContent = "Select an episode to view details"
	}

	leftHeader := "SELECT AN EPISODE"
	var selectedID string
	if m.epTable.Cursor() >= 0 && m.epTable.Cursor() < len(m.episodes) {
		selectedID = m.episodes[m.epTable.Cursor()].ID
	}
	rightHeader := "EPISODE DETAIL"
	if selectedID != "" {
		rightHeader = fmt.Sprintf("EPISODE DETAIL (%s)", selectedID)
	}

	if len(rightHeader) > rightWidth-4 {
		rightHeader = fmt.Sprintf("DETAIL (%s)", truncate(selectedID, rightWidth-14))
		if len(rightHeader) > rightWidth-4 {
			rightHeader = truncate(rightHeader, rightWidth-4)
		}
	}

	return m.drawSplitPane(leftHeader, leftContent, rightHeader, detailContent)
}

func (m model) patternsView() string {
	leftWidth := int(float64(m.width) * 0.58)
	rightWidth := m.width - leftWidth - 1
	errLines := 0
	if m.errMsg != "" {
		errLines = 2
	}
	paneHeight := m.height - 7 - errLines
	if paneHeight < 5 {
		paneHeight = 5
	}

	leftContent := m.renderPatternsList(leftWidth, paneHeight-3)

	detailContent := renderViewportWithScrollbar(m.detailVP, len(strings.Split(m.detailRaw, "\n")))
	if detailContent == "" {
		detailContent = "Select a pattern to view details"
	}

	leftHeader := "SELECT A PATTERN"
	var selectedID string
	if m.patTable.Cursor() >= 0 && m.patTable.Cursor() < len(m.patterns) {
		selectedID = m.patterns[m.patTable.Cursor()].ID
	}
	rightHeader := "PATTERN DETAIL"
	if selectedID != "" {
		rightHeader = fmt.Sprintf("PATTERN DETAIL (%s)", selectedID)
	}

	if len(rightHeader) > rightWidth-4 {
		rightHeader = fmt.Sprintf("DETAIL (%s)", truncate(selectedID, rightWidth-14))
		if len(rightHeader) > rightWidth-4 {
			rightHeader = truncate(rightHeader, rightWidth-4)
		}
	}

	return m.drawSplitPane(leftHeader, leftContent, rightHeader, detailContent)
}

func (m model) conceptsView() string {
	leftWidth := int(float64(m.width) * 0.58)
	rightWidth := m.width - leftWidth - 1
	errLines := 0
	if m.errMsg != "" {
		errLines = 2
	}
	paneHeight := m.height - 7 - errLines
	if paneHeight < 5 {
		paneHeight = 5
	}

	leftContent := m.renderConceptsList(leftWidth, paneHeight-3)

	detailContent := renderViewportWithScrollbar(m.detailVP, len(strings.Split(m.detailRaw, "\n")))
	if detailContent == "" {
		detailContent = "Select a concept to view details"
	}

	leftHeader := "SELECT CONCEPT"
	var selectedID string
	if m.conTable.Cursor() >= 0 && m.conTable.Cursor() < len(m.concepts) {
		selectedID = m.concepts[m.conTable.Cursor()].ID
	}
	rightHeader := "CONCEPT DETAIL"
	if selectedID != "" {
		rightHeader = fmt.Sprintf("CONCEPT DETAIL (%s)", selectedID)
	}

	if len(rightHeader) > rightWidth-4 {
		rightHeader = fmt.Sprintf("DETAIL (%s)", truncate(selectedID, rightWidth-14))
		if len(rightHeader) > rightWidth-4 {
			rightHeader = truncate(rightHeader, rightWidth-4)
		}
	}

	return m.drawSplitPane(leftHeader, leftContent, rightHeader, detailContent)
}

func (m model) renderASCIIGraph(conceptID string) string {
	var b strings.Builder
	// Find the concept
	var centerName string
	for _, c := range m.concepts {
		if c.ID == conceptID {
			centerName = c.EntityName
			break
		}
	}
	if centerName == "" {
		centerName = conceptID
	}

	// Find incoming and outgoing edges
	var incoming []store.GraphEdge
	var outgoing []store.GraphEdge
	for _, e := range m.graphEdges {
		if e.TargetID == conceptID {
			incoming = append(incoming, e)
		}
		if e.SourceID == conceptID {
			outgoing = append(outgoing, e)
		}
	}

	b.WriteString("  Interactive Concept Graph\n")
	b.WriteString("  " + strings.Repeat("─", 40) + "\n\n")

	// Draw incoming
	if len(incoming) > 0 {
		for _, e := range incoming {
			// Find source name
			srcName := e.SourceID
			for _, c := range m.concepts {
				if c.ID == e.SourceID {
					srcName = c.EntityName
					break
				}
			}
			fmt.Fprintf(&b, "         ┌────────────────────────┐\n")
			fmt.Fprintf(&b, "         │ %-22s │\n", truncate(srcName, 22))
			fmt.Fprintf(&b, "         └───────────┬────────────┘\n")
			fmt.Fprintf(&b, "                     │ %s (w:%.2f)\n", truncate(e.Relationship, 15), e.Weight)
			fmt.Fprintf(&b, "                     ▼\n")
		}
	} else {
		b.WriteString("             (No Incoming Edges)\n\n")
	}

	// Draw center node (highlighted)
	fmt.Fprintf(&b, "        ★ ┌────────────────────────┐ ★\n")
	fmt.Fprintf(&b, "        ★ │ %-22s │ ★ (Selected)\n", truncate(centerName, 22))
	fmt.Fprintf(&b, "        ★ └───────────┬────────────┘ ★\n")

	// Draw outgoing
	if len(outgoing) > 0 {
		for _, e := range outgoing {
			// Find target name
			tgtName := e.TargetID
			for _, c := range m.concepts {
				if c.ID == e.TargetID {
					tgtName = c.EntityName
					break
				}
			}
			fmt.Fprintf(&b, "                     │ %s (w:%.2f)\n", truncate(e.Relationship, 15), e.Weight)
			fmt.Fprintf(&b, "                     ▼\n")
			fmt.Fprintf(&b, "         ┌────────────────────────┐\n")
			fmt.Fprintf(&b, "         │ %-22s │\n", truncate(tgtName, 22))
			fmt.Fprintf(&b, "         └────────────────────────┘\n")
		}
	} else {
		b.WriteString("\n             (No Outgoing Edges)\n")
	}

	// Related memories
	var relatedEps []string
	for _, ep := range m.episodes {
		if strings.Contains(strings.ToLower(ep.Problem), strings.ToLower(centerName)) {
			relatedEps = append(relatedEps, ep.ID)
		} else {
			for _, t := range ep.Tags {
				if strings.EqualFold(t, centerName) {
					relatedEps = append(relatedEps, ep.ID)
					break
				}
			}
		}
	}

	if len(relatedEps) > 0 {
		b.WriteString("\n  " + lipgloss.NewStyle().Foreground(ghHighlight).Bold(true).Render("Related Episodes:") + "\n")
		for _, id := range relatedEps {
			b.WriteString("   • " + lipgloss.NewStyle().Foreground(ghBright).Render(id) + "\n")
		}
	}

	return b.String()
}

func (m model) graphView() string {
	if len(m.concepts) == 0 {
		return "  No concepts found to build graph\n"
	}
	leftWidth := int(float64(m.width) * 0.58)
	errLines := 0
	if m.errMsg != "" {
		errLines = 2
	}
	paneHeight := m.height - 7 - errLines
	if paneHeight < 5 {
		paneHeight = 5
	}

	// Get selected concept
	var selectedID string
	var selectedName string
	if m.conTable.Cursor() >= 0 && m.conTable.Cursor() < len(m.concepts) {
		selectedID = m.concepts[m.conTable.Cursor()].ID
		selectedName = m.concepts[m.conTable.Cursor()].EntityName
	} else if len(m.concepts) > 0 {
		selectedID = m.concepts[0].ID
		selectedName = m.concepts[0].EntityName
	}

	leftContent := m.renderConceptsList(leftWidth, paneHeight-3)
	graphStr := m.renderASCIIGraph(selectedID)

	leftHeader := "SELECT CONCEPT"
	rightHeader := "CONCEPT GRAPH"
	if selectedName != "" {
		rightHeader = fmt.Sprintf("CONCEPT GRAPH (%s)", selectedName)
	}

	return m.drawSplitPane(leftHeader, leftContent, rightHeader, graphStr)
}

func (m model) searchView() string {
	var b strings.Builder

	paneWidth := m.width - 4
	if paneWidth < 20 {
		paneWidth = 20
	}
	errLines := 0
	if m.errMsg != "" {
		errLines = 2
	}
	paneHeight := m.height - 8 - errLines
	if paneHeight < 5 {
		paneHeight = 5
	}

	paneStyle := lipgloss.NewStyle().
		Border(dashedBorder).
		BorderForeground(ghBorder).
		Width(paneWidth - 2).
		Height(paneHeight - 2)

	var content strings.Builder
	content.WriteString(lipgloss.NewStyle().Foreground(ghSubtle).Render(" SEARCH ENGINE:") + "\n\n")
	content.WriteString("  Query: " + m.searchInput.View() + "  " + lipgloss.NewStyle().Foreground(ghSubtle).Render("[Press Enter]") + "\n")
	content.WriteString("  Repo:  " + m.repoInput.View() + "  " + lipgloss.NewStyle().Foreground(ghSubtle).Render("[^F: toggle]") + "\n")

	if len(m.searchResults) > 0 {
		fmt.Fprintf(&content, "\n  %s %d Results:\n",
			lipgloss.NewStyle().Foreground(ghSubtle).Render(""),
			len(m.searchResults))
		maxResults := paneHeight - 8
		if maxResults < 2 {
			maxResults = 2
		}
		for i, r := range m.searchResults {
			if i >= maxResults {
				break
			}
			idStr := lipgloss.NewStyle().Foreground(ghHighlight).Render(r.ID)
			idLen := len(r.ID)
			availW := (m.width - 6) - (4 + idLen + 3) // "  • " (4) + ID + " | " (3)
			if availW < 10 {
				availW = 10
			}
			fmt.Fprintf(&content, "  • %s | %s\n", idStr, truncate(r.Problem, availW))
		}
	} else if m.searchInput.Value() != "" {
		content.WriteString("\n  No results found\n")
	}

	b.WriteString(paneStyle.Render(content.String()))
	return b.String()
}

func (m model) consolidationView() string {
	paneWidth := m.width - 4
	if paneWidth < 20 {
		paneWidth = 20
	}
	errLines := 0
	if m.errMsg != "" {
		errLines = 2
	}
	paneHeight := m.height - 8 - errLines
	if paneHeight < 5 {
		paneHeight = 5
	}

	paneStyle := lipgloss.NewStyle().
		Border(dashedBorder).
		BorderForeground(ghBorder).
		Width(paneWidth - 2).
		Height(paneHeight - 2)

	var content strings.Builder
	content.WriteString(lipgloss.NewStyle().Foreground(ghSubtle).Render(" CONSOLIDATION WORKBENCH:") + "\n\n")

	msgLines := paneHeight - 5
	if msgLines < 2 {
		msgLines = 2
	}
	formattedMsg := limitLines(strings.ReplaceAll(m.consolidationMsg, "\n", "\n  "), msgLines)
	content.WriteString("  " + formattedMsg + "\n")

	return paneStyle.Render(content.String())
}

func (m model) polishView() string {
	var b strings.Builder

	halfWidth := (m.width - 6) / 2
	if halfWidth < 12 {
		halfWidth = 12
	}

	boxHeight := m.getPolishBoxHeight()

	rawBoxStyle := lipgloss.NewStyle().
		Border(dashedBorder).
		BorderForeground(ghBorder).
		Width(halfWidth - 2).
		Height(boxHeight - 2)

	polishedBoxStyle := lipgloss.NewStyle().
		Border(dashedBorder).
		BorderForeground(ghHighlight).
		Width(halfWidth - 2).
		Height(boxHeight - 2)

	// Raw input string
	rawInputStr := m.polishInput.View()

	// Calculate counts
	rawChars := len(m.polishInput.Value())
	rawWords := len(strings.Fields(m.polishInput.Value()))
	rawTokens := int(float64(rawWords) * 1.3)

	var statsStr string
	if m.polishResult != nil {
		outChars := len(m.polishResult.PolishedPrompt)
		outWords := len(strings.Fields(m.polishResult.PolishedPrompt))
		outTokens := int(float64(outWords) * 1.3)
		if m.width < 75 {
			statsStr = fmt.Sprintf(
				"Raw: %dch/~%dtk | Pol: %dch/~%dtk\nType: %s | Dom: %s | Skill: %s",
				rawChars, rawTokens, outChars, outTokens,
				m.polishResult.TaskType, m.polishResult.Domain, m.polishResult.SkillName,
			)
		} else {
			statsStr = fmt.Sprintf(
				"Raw: %d chars / ~%d tokens  |  Polished: %d chars / ~%d tokens\nType: %s  |  Domain: %s  |  Skill: %s",
				rawChars, rawTokens, outChars, outTokens,
				m.polishResult.TaskType, m.polishResult.Domain, m.polishResult.SkillName,
			)
		}
	} else {
		if m.width < 40 {
			statsStr = fmt.Sprintf("Raw: %dch/~%dtk", rawChars, rawTokens)
		} else {
			statsStr = fmt.Sprintf("Raw: %d chars / ~%d tokens", rawChars, rawTokens)
		}
	}

	// Layout side-by-side — labels match HTML colors
	rawLabel := lipgloss.NewStyle().Foreground(ghSubtle).Render("RAW INPUT:")
	leftPane := rawBoxStyle.Render(fmt.Sprintf("%s\n%s", rawLabel, rawInputStr))

	polishedLabel := lipgloss.NewStyle().Foreground(ghHighlight).Bold(true).Render("POLISHED OUTPUT:")
	polishedContent := m.polishVP.View()
	if m.polishResult != nil {
		polishedContent = renderViewportWithScrollbar(
			m.polishVP,
			len(strings.Split(m.polishResult.PolishedPrompt, "\n")),
		)
	}
	rightPane := polishedBoxStyle.Render(fmt.Sprintf("%s\n%s", polishedLabel, polishedContent))

	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, leftPane, "  ", rightPane))
	if m.height < 16 {
		b.WriteString("\n  " + statsStr + "\n")
	} else {
		b.WriteString("\n\n  " + statsStr + "\n")
	}

	maxHistory := 3
	if m.height < 16 {
		maxHistory = 1
	}
	if m.height < 13 {
		maxHistory = 0
	}

	if len(m.polishHistory) > 0 && maxHistory > 0 {
		if m.height < 16 {
			fmt.Fprintf(&b, "  History (%d):\n", len(m.polishHistory))
		} else {
			fmt.Fprintf(&b, "\n  History (%d):\n", len(m.polishHistory))
		}
		start := len(m.polishHistory) - maxHistory
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

	paneWidth := m.width - 4
	if paneWidth < 20 {
		paneWidth = 20
	}
	errLines := 0
	if m.errMsg != "" {
		errLines = 2
	}
	paneHeight := m.height - 8 - errLines
	if paneHeight < 5 {
		paneHeight = 5
	}

	paneStyle := lipgloss.NewStyle().
		Border(dashedBorder).
		BorderForeground(ghBorder).
		Width(paneWidth - 2).
		Height(paneHeight - 2)

	var content strings.Builder
	content.WriteString(lipgloss.NewStyle().Foreground(ghSubtle).Render(" SYSTEM STATISTICS:") + "\n\n")

	formatPercent := func(val float64) string {
		for val > 100.0 {
			val = val / 100.0
		}
		if val > 0 && val <= 1.0 {
			val = val * 100.0
		}
		return fmt.Sprintf("%.1f%%", val)
	}

	successRate := formatPercent(m.statsData.SuccessRate)
	consRatio := formatPercent(m.statsData.ConsolidationRatio)

	if paneWidth < 55 {
		rowSingle := func(label string, val string, valColor lipgloss.Color) {
			fmt.Fprintf(&content, "  %s: %s\n", label, lipgloss.NewStyle().Foreground(valColor).Render(val))
		}
		rowSingle("Total Episodes", fmt.Sprintf("%d", m.statsData.EpisodesTotal), ghHighlight)
		rowSingle("Total Patterns", fmt.Sprintf("%d", m.statsData.PatternsTotal), ghPurple)
		rowSingle("Database Size", fmt.Sprintf("%.2f MB", m.statsData.DBSizeMB), lipgloss.Color("#c9d1d9"))
		rowSingle("FTS5 Index Size", fmt.Sprintf("%.2f MB", m.statsData.FTSSizeMB), lipgloss.Color("#c9d1d9"))
		rowSingle("Success Rate", successRate, ghBrightGrn)
		rowSingle("Consolidation Ratio", consRatio, ghOrange)
		if m.statsData.TopDomain != "" {
			topRepo := m.statsData.TopRepo
			if topRepo == "" {
				topRepo = "N/A"
			}
			rowSingle("Top Domain", m.statsData.TopDomain, lipgloss.Color("#c9d1d9"))
			rowSingle("Top Repo", topRepo, lipgloss.Color("#c9d1d9"))
		}
		maybeNA := func(v float64) string {
			if v == 0 {
				return "N/A"
			}
			return fmt.Sprintf("%.1f s", v)
		}
		rowSingle("Avg Duration", maybeNA(m.statsData.AvgDurationSec), lipgloss.Color("#c9d1d9"))
		avgLen := "N/A"
		if m.statsData.AvgEpisodeLenChars != 0 {
			avgLen = fmt.Sprintf("%.0f chars", m.statsData.AvgEpisodeLenChars)
		}
		rowSingle("Avg Episode Len", avgLen, lipgloss.Color("#c9d1d9"))
	} else {
		colW := (paneWidth - 4) / 2
		row := func(label1 string, val1 string, val1Color lipgloss.Color, label2 string, val2 string, val2Color lipgloss.Color) {
			c1 := fmt.Sprintf("  %s: %s", label1, lipgloss.NewStyle().Foreground(val1Color).Render(val1))
			c2 := fmt.Sprintf("  %s: %s", label2, lipgloss.NewStyle().Foreground(val2Color).Render(val2))
			padLen := colW - lipgloss.Width(c1)
			if padLen < 1 {
				padLen = 1
			}
			content.WriteString(c1 + strings.Repeat(" ", padLen) + c2 + "\n")
		}
		row("Total Episodes", fmt.Sprintf("%d", m.statsData.EpisodesTotal), ghHighlight,
			"Total Patterns", fmt.Sprintf("%d", m.statsData.PatternsTotal), ghPurple)
		row("Database Size", fmt.Sprintf("%.2f MB", m.statsData.DBSizeMB), lipgloss.Color("#c9d1d9"),
			"FTS5 Index Size", fmt.Sprintf("%.2f MB", m.statsData.FTSSizeMB), lipgloss.Color("#c9d1d9"))
		row("Success Rate", successRate, ghBrightGrn,
			"Consolidation Ratio", consRatio, ghOrange)
		if m.statsData.TopDomain != "" {
			topRepo := m.statsData.TopRepo
			if topRepo == "" {
				topRepo = "N/A"
			}
			row("Top Domain", m.statsData.TopDomain, lipgloss.Color("#c9d1d9"),
				"Top Repo", topRepo, lipgloss.Color("#c9d1d9"))
		}
		maybeNA := func(v float64) string {
			if v == 0 {
				return "N/A"
			}
			return fmt.Sprintf("%.1f s", v)
		}
		row("Avg Duration", maybeNA(m.statsData.AvgDurationSec), lipgloss.Color("#c9d1d9"),
			"Avg Episode Len", func() string {
				if m.statsData.AvgEpisodeLenChars == 0 {
					return "N/A"
				}
				return fmt.Sprintf("%.0f chars", m.statsData.AvgEpisodeLenChars)
			}(), lipgloss.Color("#c9d1d9"))
	}

	b.WriteString(paneStyle.Render(content.String()))

	return b.String()
}

func renderScrollableListLine(line string, selected bool, row, start, total, viewportHeight, width int) string {
	if width < 2 {
		width = 2
	}

	track := " "
	if total > viewportHeight && viewportHeight > 0 {
		thumbSize := viewportHeight * viewportHeight / total
		if thumbSize < 1 {
			thumbSize = 1
		}
		if thumbSize > viewportHeight {
			thumbSize = viewportHeight
		}
		maxStart := total - viewportHeight
		thumbStart := 0
		if maxStart > 0 {
			thumbStart = start * (viewportHeight - thumbSize) / maxStart
		}
		if row >= thumbStart && row < thumbStart+thumbSize {
			track = lipgloss.NewStyle().Foreground(ghOrange).Render("█")
		} else {
			track = lipgloss.NewStyle().Foreground(ghBorder).Render("░")
		}
	}

	contentWidth := width - 1
	if contentWidth < 1 {
		contentWidth = 1
	}
	line = ansi.TruncateWc(line, contentWidth, "…")
	lineWidth := lipgloss.Width(line)
	if lineWidth < contentWidth {
		line += strings.Repeat(" ", contentWidth-lineWidth)
	}
	if selected {
		line = lipgloss.NewStyle().Background(ghDarkBg).Render(line)
	}
	return line + track
}

func renderViewportWithScrollbar(vp viewport.Model, _ int) string {
	content := vp.View()
	totalLines := vp.TotalLineCount()
	if vp.Height <= 0 || totalLines <= vp.Height {
		return content
	}

	lines := strings.Split(content, "\n")
	height := vp.Height
	if len(lines) < height {
		height = len(lines)
	}
	thumbSize := height * height / totalLines
	if thumbSize < 1 {
		thumbSize = 1
	}
	maxOffset := totalLines - vp.Height
	thumbStart := 0
	if maxOffset > 0 {
		thumbStart = vp.YOffset * (height - thumbSize) / maxOffset
	}

	var b strings.Builder
	for i := 0; i < height; i++ {
		line := lines[i]
		if pad := vp.Width - lipgloss.Width(line) - 1; pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		track := lipgloss.NewStyle().Foreground(ghBorder).Render("░")
		if i >= thumbStart && i < thumbStart+thumbSize {
			track = lipgloss.NewStyle().Foreground(ghOrange).Render("█")
		}
		b.WriteString(line + track)
		if i < height-1 {
			b.WriteByte('\n')
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

func limitLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n") + "\n  ... (truncated)"
}
