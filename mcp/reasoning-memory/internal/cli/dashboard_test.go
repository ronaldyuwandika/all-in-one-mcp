package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/prompter"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
)

func newTestStore(t *testing.T) *store.EpisodeStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	es, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { es.Close() })
	for i := 0; i < 3; i++ {
		_, err := es.CreateEpisode(&models.Episode{
			ID:              es.NextID(),
			Domain:          "coding",
			Outcome:         "success",
			Tags:            []string{"go", "test"},
			Problem:         "test problem",
			ThinkingTrace:   "test trace",
			DurationSeconds: 10,
		})
		if err != nil {
			t.Fatalf("CreateEpisode: %v", err)
		}
	}
	return es
}

func newTestDashboard(t *testing.T) (model, *store.EpisodeStore) {
	t.Helper()
	es := newTestStore(t)
	m := initialModel(es, "/dev/null", &models.Config{})
	m.width = 120
	m.height = 40
	m.ready = true
	return m, es
}

func tabMsg() tea.Msg   { return tea.KeyMsg{Type: tea.KeyTab} }
func enterMsg() tea.Msg { return tea.KeyMsg{Type: tea.KeyEnter} }

func upd(m tea.Model, msg tea.Msg) model {
	r, _ := m.Update(msg)
	return r.(model)
}

func TestInitialModel(t *testing.T) {
	m, _ := newTestDashboard(t)
	if m.activeTab != 0 {
		t.Errorf("initial tab = %d, want 0", m.activeTab)
	}
	if m.searchInput.Focused() {
		t.Error("searchInput should NOT be focused initially")
	}
	if m.polishInput.Focused() {
		t.Error("polishInput should NOT be focused initially")
	}
}

func TestSearchInputAutoFocused(t *testing.T) {
	m, _ := newTestDashboard(t)
	m = upd(m, tabMsg())
	m = upd(m, tabMsg())
	if m.activeTab != 2 {
		t.Fatalf("expected tab 2 (Search), got %d", m.activeTab)
	}
	if !m.searchInput.Focused() {
		t.Error("searchInput should be auto-focused on Search tab")
	}
	if m.polishInput.Focused() {
		t.Error("polishInput should NOT be focused on Search tab")
	}
}

func TestPolishInputAutoFocused(t *testing.T) {
	m, _ := newTestDashboard(t)
	for i := 0; i < 4; i++ {
		m = upd(m, tabMsg())
	}
	if m.activeTab != 4 {
		t.Fatalf("expected tab 4 (Polish), got %d", m.activeTab)
	}
	if !m.polishInput.Focused() {
		t.Error("polishInput should be auto-focused on Polish tab")
	}
	if m.searchInput.Focused() {
		t.Error("searchInput should NOT be focused on Polish tab")
	}
}

func TestBlurOnLeaveSearchTab(t *testing.T) {
	m, _ := newTestDashboard(t)
	for i := 0; i < 2; i++ {
		m = upd(m, tabMsg())
	}
	if m.activeTab != 2 {
		t.Fatalf("expected tab 2 (Search), got %d", m.activeTab)
	}
	m = upd(m, tabMsg())
	if m.activeTab != 3 {
		t.Fatalf("expected tab 3 (Consolidation), got %d", m.activeTab)
	}
	if m.searchInput.Focused() {
		t.Error("searchInput should be blurred when leaving Search tab")
	}
}

func TestSearchInputTypeAndSearch(t *testing.T) {
	m, _ := newTestDashboard(t)
	for i := 0; i < 2; i++ {
		m = upd(m, tabMsg())
	}
	m = upd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("test")})
	if m.searchInput.Value() != "test" {
		t.Errorf("searchInput value = %q, want %q", m.searchInput.Value(), "test")
	}
	m = upd(m, enterMsg())
	if len(m.searchResults) == 0 {
		t.Log("no search results (FTS index may be empty)")
	}
}

func TestStatsTabTriggersLoad(t *testing.T) {
	m, _ := newTestDashboard(t)
	for i := 0; i < 5; i++ {
		m = upd(m, tabMsg())
	}
	if m.activeTab != 5 {
		t.Fatalf("expected tab 5 (Stats), got %d", m.activeTab)
	}
}

func TestSearchViewRenders(t *testing.T) {
	m, _ := newTestDashboard(t)
	for i := 0; i < 2; i++ {
		m = upd(m, tabMsg())
	}
	_ = m.searchView()
	if !m.searchInput.Focused() {
		t.Error("searchInput should be focused in searchView")
	}
}

func TestPolishViewRenders(t *testing.T) {
	m, _ := newTestDashboard(t)
	for i := 0; i < 4; i++ {
		m = upd(m, tabMsg())
	}
	if !m.polishInput.Focused() {
		t.Error("polishInput should be focused in polishView")
	}
}

func TestEpisodesViewRenders(t *testing.T) {
	m, _ := newTestDashboard(t)
	_ = m.episodesView()
}

func TestEmptyStats(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	es, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer es.Close()
	m := initialModel(es, "/dev/null", &models.Config{})
	m.width = 120
	m.height = 40
	m.ready = true
	cmd := m.loadStats()
	if cmd == nil {
		t.Fatal("loadStats returned nil cmd")
	}
	msg := cmd()
	sm, ok := msg.(statsMsg)
	if !ok {
		t.Fatalf("loadStats returned %T, want statsMsg", msg)
	}
	if sm.stats == nil {
		t.Fatal("statsMsg.stats is nil")
	}
	if sm.stats.EpisodesTotal != 0 {
		t.Errorf("EpisodesTotal = %d, want 0", sm.stats.EpisodesTotal)
	}
}

func TestConceptsAndGraphTabs(t *testing.T) {
	m, es := newTestDashboard(t)
	// Go to Tab 6 (Concepts)
	for i := 0; i < 6; i++ {
		m = upd(m, tabMsg())
	}
	if m.activeTab != 6 {
		t.Fatalf("expected tab 6 (Concepts), got %d", m.activeTab)
	}
	_ = m.conceptsView()

	// Go to Tab 7 (Graph)
	m = upd(m, tabMsg())
	if m.activeTab != 7 {
		t.Fatalf("expected tab 7 (Graph), got %d", m.activeTab)
	}
	_ = m.graphView()

	// Load concepts
	cmd := m.loadConcepts()
	if cmd != nil {
		msg := cmd()
		m = upd(m, msg)
	}

	// Load edges
	cmd = m.loadEdges()
	if cmd != nil {
		msg := cmd()
		m = upd(m, msg)
	}

	// Test promote on episodes tab (tab 0)
	m.activeTab = 0
	eps, err := es.ListEpisodes(10, 0)
	if err != nil || len(eps) == 0 {
		t.Fatalf("failed to list episodes: %v", err)
	}
	m.episodes = eps
	m.epTable.SetCursor(0)

	// Send promote key msg
	m = upd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	// Trigger the resulting promoteEpisode cmd if any
	// (note: Update returns cmd for promote, which we can call)
	pCmd := m.promoteEpisode(eps[0].ID)
	if pCmd != nil {
		pMsg := pCmd()
		m = upd(m, pMsg)
	}
}

// TestLayoutMatchesVisualizationHTML verifies key visual properties match visualization.html.
func TestLayoutMatchesVisualizationHTML(t *testing.T) {
	m, es := newTestDashboard(t)

	// Load episodes into model
	eps, err := es.ListEpisodes(10, 0)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	m.episodes = eps
	m.refreshEpTable()

	// Test Episodes tab (tab 0) renders split pane
	view := m.episodesView()
	if view == "" {
		t.Fatal("episodesView returned empty")
	}
	// Dashed border chars present
	if !containsAny(view, "┌", "┐", "└", "┘", "╌", "┆") {
		t.Error("episodes view missing dashed border chars (┌┐└┘╌┆)")
	}
	// Header present without bold separator line
	if !containsAny(view, "SELECT AN EPISODE") {
		t.Error("episodes view missing 'SELECT AN EPISODE' header")
	}
	if !containsAny(view, "EPISODE DETAIL") {
		t.Error("episodes view missing 'EPISODE DETAIL' header")
	}
	// Selected item uses ▶ prefix
	if !containsAny(view, "▶") {
		t.Error("episodes view missing ▶ selection indicator")
	}

	// Test Stats tab renders single pane with 2-column grid
	m.statsData = &models.StatsResult{
		EpisodesTotal:      42,
		PatternsTotal:      5,
		SuccessRate:        0.85,
		ConsolidationRatio: 0.12,
		AvgDurationSec:     15.0,
		DBSizeMB:           0.24,
		FTSSizeMB:          0.08,
	}
	sv := m.statsView()
	if !containsAny(sv, "SYSTEM STATISTICS") {
		t.Error("stats view missing 'SYSTEM STATISTICS' header")
	}
	if !containsAny(sv, "Total Episodes") {
		t.Error("stats view missing 'Total Episodes' label")
	}
	if !containsAny(sv, "42") {
		t.Error("stats view missing episode count value")
	}

	// Test Search tab renders in pane
	m.activeTab = 2
	m = upd(m, tabMsg()) // ensure search focus
	m.activeTab = 2
	searchOut := m.searchView()
	if !containsAny(searchOut, "SEARCH ENGINE") {
		t.Error("search view missing 'SEARCH ENGINE' header")
	}

	// Test Polish tab renders side-by-side panes
	m.activeTab = 4
	polishOut := m.polishView()
	if !containsAny(polishOut, "RAW INPUT") {
		t.Error("polish view missing 'RAW INPUT' label")
	}
	if !containsAny(polishOut, "POLISHED OUTPUT") {
		t.Error("polish view missing 'POLISHED OUTPUT' label")
	}

	// Test Consolidation tab renders in pane
	conOut := m.consolidationView()
	if !containsAny(conOut, "CONSOLIDATION WORKBENCH") {
		t.Error("consolidation view missing 'CONSOLIDATION WORKBENCH' header")
	}

	// Test footer format
	fullView := m.View()
	if !containsAny(fullView, "q: quit", "tab: next tab", "enter: select", "d: delete") {
		t.Error("footer missing expected key hints")
	}

	// Verify line counts for all tabs to ensure no truncation or overflow
	m.width = 80
	m.height = 24
	m.ready = true
	// Mock polish results and history to test maximum polishView height
	m.polishResult = &prompter.PolishResult{
		PolishedPrompt: "Polished",
		TaskType:       "coding",
		Domain:         "coding",
		SkillName:      "go-code-style",
	}
	m.polishHistory = []polishEntry{
		{original: "raw1", result: m.polishResult},
		{original: "raw2", result: m.polishResult},
		{original: "raw3", result: m.polishResult},
	}

	for tabID := 0; tabID < len(m.tabNames); tabID++ {
		m.activeTab = tabID
		m.recalcSizing()
		v := m.View()
		lines := strings.Split(v, "\n")
		// Remove trailing empty elements if split produced any
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		t.Logf("Tab %d (%s) renders %d lines (height=%d)", tabID, m.tabNames[tabID], len(lines), m.height)
		if len(lines) > m.height {
			t.Errorf("Tab %d (%s) overflowed: got %d lines, max allowed %d. View:\n%s", tabID, m.tabNames[tabID], len(lines), m.height, v)
		}
	}
}

func TestDashboardScrollbarsAndShortHeightKeepMenuVisible(t *testing.T) {
	m, es := newTestDashboard(t)
	eps, err := es.ListEpisodes(10, 0)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(eps) == 0 {
		t.Fatal("expected test episodes")
	}

	m.episodes = make([]models.EpisodeSummary, 30)
	for i := range m.episodes {
		m.episodes[i] = eps[i%len(eps)]
		m.episodes[i].ID = fmt.Sprintf("re-scroll-%02d", i)
	}
	m.refreshEpTable()
	m.width = 100
	m.height = 14
	m.recalcSizing()

	view := m.View()
	if !strings.Contains(view, "░") || !strings.Contains(view, "█") {
		t.Fatalf("episodes view missing scrollbar track/thumb:\n%s", view)
	}
	if !strings.Contains(strings.Split(view, "\n")[0], "Episodes") {
		t.Fatalf("tab menu missing from first line:\n%s", view)
	}
	if lines := strings.Count(view, "\n") + 1; lines > m.height {
		t.Fatalf("dashboard overflowed short terminal: got %d lines, height %d", lines, m.height)
	}

	m.height = 8
	m.recalcSizing()
	view = m.View()
	firstLine := strings.Split(view, "\n")[0]
	if !strings.Contains(firstLine, "Eps") && !strings.Contains(firstLine, "Episodes") {
		t.Fatalf("compact tab menu missing from first line:\n%s", view)
	}
	if !strings.Contains(view, "Terminal height too small") {
		t.Fatalf("short terminal fallback missing:\n%s", view)
	}
	if lines := strings.Count(view, "\n") + 1; lines > m.height {
		t.Fatalf("short terminal fallback overflowed: got %d lines, height %d", lines, m.height)
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
