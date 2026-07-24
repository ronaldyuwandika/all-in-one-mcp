package store

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/security"
)

type Metrics struct {
	EpisodesCaptured  atomic.Int64
	SearchesPerformed atomic.Int64
	ConsolidationsRan atomic.Int64
	ErrorsTotal       atomic.Int64
	ConceptsMemorized atomic.Int64
	EdgesCreated      atomic.Int64

	SearchDurations   durationTracker
	ConsolidationDurs durationTracker
	CaptureDurations  durationTracker
}

type durationTracker struct {
	total atomic.Int64
	count atomic.Int64
	max   atomic.Int64
}

func (d *durationTracker) Record(dur time.Duration) {
	ms := dur.Milliseconds()
	d.total.Add(ms)
	d.count.Add(1)
	for {
		old := d.max.Load()
		if ms <= old || d.max.CompareAndSwap(old, ms) {
			break
		}
	}
}

func (d *durationTracker) Stats() (avg, max, count int64) {
	count = d.count.Load()
	if count > 0 {
		avg = d.total.Load() / count
	}
	max = d.max.Load()
	return
}

var GlobalMetrics = &Metrics{}

func MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		m := GlobalMetrics

		fmt.Fprintf(w, "# HELP rmn_episodes_captured Episodes captured\n")
		fmt.Fprintf(w, "# TYPE rmn_episodes_captured counter\n")
		fmt.Fprintf(w, "rmn_episodes_captured %d\n\n", m.EpisodesCaptured.Load())

		fmt.Fprintf(w, "# HELP rmn_searches_performed Search invocations\n")
		fmt.Fprintf(w, "# TYPE rmn_searches_performed counter\n")
		fmt.Fprintf(w, "rmn_searches_performed %d\n\n", m.SearchesPerformed.Load())

		fmt.Fprintf(w, "# HELP rmn_consolidations_ran Consolidations executed\n")
		fmt.Fprintf(w, "# TYPE rmn_consolidations_ran counter\n")
		fmt.Fprintf(w, "rmn_consolidations_ran %d\n\n", m.ConsolidationsRan.Load())

		fmt.Fprintf(w, "# HELP rmn_errors_total Total errors by operation\n")
		fmt.Fprintf(w, "# TYPE rmn_errors_total counter\n")
		fmt.Fprintf(w, "rmn_errors_total %d\n\n", m.ErrorsTotal.Load())

		fmt.Fprintf(w, "# HELP rmn_concepts_memorized Concepts memorized\n")
		fmt.Fprintf(w, "# TYPE rmn_concepts_memorized counter\n")
		fmt.Fprintf(w, "rmn_concepts_memorized %d\n\n", m.ConceptsMemorized.Load())

		fmt.Fprintf(w, "# HELP rmn_edges_created Graph edges created\n")
		fmt.Fprintf(w, "# TYPE rmn_edges_created counter\n")
		fmt.Fprintf(w, "rmn_edges_created %d\n\n", m.EdgesCreated.Load())

		avg, max, count := m.SearchDurations.Stats()
		fmt.Fprintf(w, "# HELP rmn_search_duration_ms Search latency stats\n")
		fmt.Fprintf(w, "# TYPE rmn_search_duration_ms gauge\n")
		fmt.Fprintf(w, "rmn_search_duration_ms_avg %d\n", avg)
		fmt.Fprintf(w, "rmn_search_duration_ms_max %d\n", max)
		fmt.Fprintf(w, "rmn_search_duration_ms_count %d\n\n", count)

		avg, max, count = m.ConsolidationDurs.Stats()
		fmt.Fprintf(w, "# HELP rmn_consolidation_duration_ms Consolidation latency stats\n")
		fmt.Fprintf(w, "# TYPE rmn_consolidation_duration_ms gauge\n")
		fmt.Fprintf(w, "rmn_consolidation_duration_ms_avg %d\n", avg)
		fmt.Fprintf(w, "rmn_consolidation_duration_ms_max %d\n", max)
		fmt.Fprintf(w, "rmn_consolidation_duration_ms_count %d\n\n", count)

		avg, max, count = m.CaptureDurations.Stats()
		fmt.Fprintf(w, "# HELP rmn_capture_duration_ms Capture latency stats\n")
		fmt.Fprintf(w, "# TYPE rmn_capture_duration_ms gauge\n")
		fmt.Fprintf(w, "rmn_capture_duration_ms_avg %d\n", avg)
		fmt.Fprintf(w, "rmn_capture_duration_ms_max %d\n", max)
		fmt.Fprintf(w, "rmn_capture_duration_ms_count %d\n\n", count)

		if es := getGlobalStore(); es != nil {
			dbSize, _ := es.DBSizeMB()
			fmt.Fprintf(w, "# HELP rmn_store_size_bytes Store sizes\n")
			fmt.Fprintf(w, "# TYPE rmn_store_size_bytes gauge\n")
			fmt.Fprintf(w, "rmn_store_db_size_mb %.2f\n", dbSize)
			ftsSize, _ := es.FTSSizeMB()
			fmt.Fprintf(w, "rmn_store_fts_size_mb %.2f\n\n", ftsSize)
		}
	})
}

var globalStore atomic.Pointer[EpisodeStore]

func SetGlobalStore(es *EpisodeStore) {
	globalStore.Store(es)
}

func getGlobalStore() *EpisodeStore {
	return globalStore.Load()
}

type TracingCtx struct {
	ctx    context.Context
	spanID string
	start  time.Time
}

func StartTrace(ctx context.Context, operation string) (context.Context, *TracingCtx) {
	spanID := fmt.Sprintf("%s-%d", operation, time.Now().UnixNano())
	tc := &TracingCtx{ctx: ctx, spanID: spanID, start: time.Now()}
	slog.DebugContext(ctx, "span_started",
		"operation", operation,
		"span_id", spanID,
	)
	return ctx, tc
}

func (t *TracingCtx) End(err error) {
	dur := time.Since(t.start)
	attrs := []any{
		"span_id", t.spanID,
		"duration_ms", dur.Milliseconds(),
	}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
		slog.ErrorContext(t.ctx, "span_ended_error", attrs...)
	} else {
		slog.DebugContext(t.ctx, "span_ended", attrs...)
	}
}

func SetupLogger() {
	base := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(redactingHandler{next: base}))
}

type redactingHandler struct {
	next slog.Handler
}

func (h redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, security.Text(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clean.AddAttrs(redactAttr(attr))
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		clean[i] = redactAttr(attr)
	}
	return redactingHandler{next: h.next.WithAttrs(clean)}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{next: h.next.WithGroup(security.Text(name))}
}

func redactAttr(attr slog.Attr) slog.Attr {
	attr.Key = security.Text(attr.Key)
	switch attr.Value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(security.Text(attr.Value.String()))
	case slog.KindGroup:
		group := attr.Value.Group()
		for i := range group {
			group[i] = redactAttr(group[i])
		}
		attr.Value = slog.GroupValue(group...)
	case slog.KindAny:
		switch value := attr.Value.Any().(type) {
		case error:
			attr.Value = slog.StringValue(security.Text(value.Error()))
		case string:
			attr.Value = slog.StringValue(security.Text(value))
		}
	}
	return attr
}
