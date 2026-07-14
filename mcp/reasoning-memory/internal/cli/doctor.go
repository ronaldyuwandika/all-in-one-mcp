package cli

import (
	"database/sql"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/config"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/store"
	"github.com/spf13/cobra"
)

func NewDoctorCmd(es *store.EpisodeStore, cfgPath string) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run health checks on the reasoning-memory system",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			exitCode := runDoctor(es, cfgPath)
			os.Exit(exitCode)
			return nil
		},
	}
}

func runDoctor(es *store.EpisodeStore, cfgPath string) int {
	statusOK := 0 // 0 = healthy, 1 = warnings, 2 = critical

	report := func(check, status, msg string, critical bool) {
		icon := "[OK]   "
		if status == "warn" {
			icon = "[WARN] "
			if !critical && statusOK < 2 {
				statusOK = 1
			}
		}
		if status == "fail" {
			icon = "[FAIL] "
			statusOK = 2
		}
		fmt.Printf("  %s %s: %s\n", icon, check, msg)
	}

	checkSQLite(es, report)
	checkFTS5(es, report)
	checkVector(es, report)
	checkConfig(cfgPath, report)
	checkEmptyTraces(es, report)
	checkConsolidation(es, report)
	checkDiskSpace(es, report)

	switch statusOK {
	case 0:
		fmt.Println("\n  ✓ All checks passed")
	case 1:
		fmt.Println("\n  ⚠ Warnings present")
	default:
		fmt.Println("\n  ✗ Critical failures detected")
	}

	return statusOK
}

func checkSQLite(es *store.EpisodeStore, report func(string, string, string, bool)) {
	db, err := sql.Open("sqlite", es.DBPath())
	if err != nil {
		report("SQLite DB", "fail", fmt.Sprintf("open failed: %v", err), true)
		return
	}
	defer func() { _ = db.Close() }()

	if err := db.Ping(); err != nil {
		report("SQLite DB", "fail", fmt.Sprintf("ping failed: %v", err), true)
		return
	}

	var journalMode string
	_ = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if journalMode != "wal" {
		report("SQLite DB (journal_mode)", "warn",
			fmt.Sprintf("expected 'wal', got '%s'", journalMode), false)
	} else {
		report("SQLite DB", "ok", fmt.Sprintf("accessible (WAL mode, path=%s)", es.DBPath()), false)
	}
}

func checkFTS5(es *store.EpisodeStore, report func(string, string, string, bool)) {
	var result string
	err := es.DB().QueryRow("PRAGMA integrity_check('episodes_fts')").Scan(&result)
	if err != nil {
		var fallback string
		ferr := es.DB().QueryRow("SELECT COUNT(*) FROM episodes_fts WHERE episodes_fts MATCH 'test'").Scan(&fallback)
		if ferr != nil {
			report("FTS5 index", "warn", fmt.Sprintf("integrity check unavailable (%v), FTS5 query test: %v", err, ferr), false)
			return
		}
		report("FTS5 index", "ok", "index query succeeded (integrity check unsupported)", false)
		return
	}
	if result != "ok" {
		report("FTS5 index", "fail", fmt.Sprintf("integrity check: %s", result), true)
	} else {
		report("FTS5 index", "ok", "integrity check passed", false)
	}
}

func checkVector(es *store.EpisodeStore, report func(string, string, string, bool)) {
	vs := es.VectorStore()
	if vs == nil {
		report("Vector store", "ok", "disabled (not configured)", false)
		return
	}
	if !vs.Enabled() {
		report("Vector store", "ok", "disabled (enabled=false)", false)
		return
	}
	count := vs.Count()
	report("Vector store", "ok", fmt.Sprintf("reachable, %d documents indexed (%s)", count, vs.Provider()), false)
}

func checkConfig(cfgPath string, report func(string, string, string, bool)) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		report("Config file", "fail", fmt.Sprintf("load failed: %v", err), true)
		return
	}
	if cfg.Version == "" {
		report("Config file", "fail", "missing version field", true)
		return
	}
	report("Config file", "ok", fmt.Sprintf("valid (path=%s)", cfgPath), false)
}

func checkEmptyTraces(es *store.EpisodeStore, report func(string, string, string, bool)) {
	count, err := es.EmptyThinkingTraceCount()
	if err != nil {
		report("Thinking traces", "warn", fmt.Sprintf("could not check: %v", err), false)
		return
	}
	if count > 0 {
		report("Thinking traces", "warn",
			fmt.Sprintf("%d episode(s) with empty thinking_trace", count), false)
	} else {
		report("Thinking traces", "ok", "all episodes have non-empty thinking_trace", false)
	}
}

func checkConsolidation(es *store.EpisodeStore, report func(string, string, string, bool)) {
	ts, err := es.LastConsolidationTS()
	if err != nil || ts == nil {
		report("Consolidation", "warn", "no consolidation run found (never consolidated)", false)
		return
	}
	since := time.Since(*ts)
	if since > 7*24*time.Hour {
		days := int(since.Hours() / 24)
		report("Consolidation", "warn",
			fmt.Sprintf("last run %d days ago (threshold: 7d)", days), false)
	} else {
		hours := int(since.Hours())
		report("Consolidation", "ok",
			fmt.Sprintf("last run %d hours ago", hours), false)
	}
}

func checkDiskSpace(es *store.EpisodeStore, report func(string, string, string, bool)) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(es.DBPath(), &stat); err != nil {
		report("Disk space", "warn", fmt.Sprintf("could not check: %v", err), false)
		return
	}
	freeGB := float64(stat.Bavail*uint64(stat.Bsize)) / 1024 / 1024 / 1024
	if freeGB < 1 {
		report("Disk space", "warn",
			fmt.Sprintf("low: %.1f GB free (< 1 GB threshold)", freeGB), false)
	} else {
		report("Disk space", "ok",
			fmt.Sprintf("%.1f GB free", freeGB), false)
	}
}
