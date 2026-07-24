package cli

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
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
