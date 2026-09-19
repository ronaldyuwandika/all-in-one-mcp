package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/models"
)

func TestInjectCmd(t *testing.T) {
	es := newTestStore(t)
	cfg := &models.Config{}

	t.Run("flags plain text", func(t *testing.T) {
		cmd := NewInjectCmd(es, cfg)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--problem", "test problem"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		if !strings.Contains(out.String(), "<reasoning_memory>") {
			t.Fatalf("expected <reasoning_memory>, got %s", out.String())
		}
	})

	t.Run("stdin json", func(t *testing.T) {
		cmd := NewInjectCmd(es, cfg)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetIn(strings.NewReader("test problem from stdin"))
		cmd.SetArgs([]string{"--json"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		var resp InjectOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal json: %v, raw: %s", err, out.String())
		}
		if !strings.Contains(resp.Context, "<reasoning_memory>") {
			t.Fatalf("expected <reasoning_memory> in context, got %s", resp.Context)
		}
	})
}

func TestRetrieveCmd(t *testing.T) {
	es := newTestStore(t)
	cfg := &models.Config{}

	t.Run("flags json output", func(t *testing.T) {
		cmd := NewRetrieveCmd(es, cfg)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--problem", "test", "--json"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		var results []models.EpisodeSummary
		if err := json.Unmarshal(out.Bytes(), &results); err != nil {
			t.Fatalf("unmarshal json: %v, raw: %s", err, out.String())
		}
		if len(results) == 0 {
			t.Fatalf("expected at least 1 result, got 0")
		}
	})

	t.Run("stdin text output", func(t *testing.T) {
		cmd := NewRetrieveCmd(es, cfg)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetIn(strings.NewReader("test"))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		if !strings.Contains(out.String(), "Found") {
			t.Fatalf("expected Found in output, got %s", out.String())
		}
	})
}

func TestCaptureCmd(t *testing.T) {
	es := newTestStore(t)
	cfg := &models.Config{}

	t.Run("flags text output", func(t *testing.T) {
		cmd := NewCaptureCmd(es, cfg)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{
			"--problem", "new problem",
			"--thinking-trace", "1. decide to do something",
			"--outcome", "success",
			"--tags", "go,cli",
		})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		id := strings.TrimSpace(out.String())
		if !strings.HasPrefix(id, "re-") {
			t.Fatalf("expected episode id starting with re-, got %s", id)
		}
	})

	t.Run("stdin json preserves fields", func(t *testing.T) {
		cmd := NewCaptureCmd(es, cfg)
		var out bytes.Buffer
		cmd.SetOut(&out)
		payload := `{"problem":"json stdin problem","thinking_trace":"trace text","outcome":"partial","domain":"agentic","tier":"semantic","tags":["json"]}`
		cmd.SetIn(strings.NewReader(payload))
		cmd.SetArgs([]string{"--json"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		var resp CaptureOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal json: %v, raw: %s", err, out.String())
		}
		if !strings.HasPrefix(resp.ID, "re-") {
			t.Fatalf("expected id starting with re-, got %s", resp.ID)
		}
		if resp.Status != "captured" || resp.Outcome != "partial" {
			t.Fatalf("unexpected capture response: %+v", resp)
		}
		ep, err := es.GetEpisode(resp.ID)
		if err != nil {
			t.Fatalf("get episode: %v", err)
		}
		if ep.Problem != "json stdin problem" || ep.Domain != "agentic" || ep.Tier != models.TierSemantic || ep.Outcome != models.OutcomePartialSuccess {
			t.Fatalf("JSON fields were not preserved: %+v", ep)
		}
	})

	t.Run("explicit flags override json", func(t *testing.T) {
		cmd := NewCaptureCmd(es, cfg)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetIn(strings.NewReader(`{"problem":"json problem","outcome":"failure","domain":"agentic","tier":"semantic"}`))
		cmd.SetArgs([]string{
			"--problem", "flag problem",
			"--outcome", "success",
			"--domain", "coding",
			"--tier", "episodic",
			"--json",
		})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		var resp CaptureOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal json: %v", err)
		}
		ep, err := es.GetEpisode(resp.ID)
		if err != nil {
			t.Fatalf("get episode: %v", err)
		}
		if ep.Problem != "flag problem" || ep.Domain != "coding" || ep.Tier != models.TierEpisodic || ep.Outcome != models.OutcomeUnverifiedSuccess {
			t.Fatalf("explicit flags did not override JSON: %+v", ep)
		}
	})

	t.Run("flag only invocation does not read terminal stdin", func(t *testing.T) {
		cmd := NewCaptureCmd(es, cfg)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--problem", "flag only"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		if !strings.HasPrefix(strings.TrimSpace(out.String()), "re-") {
			t.Fatalf("expected episode id, got %q", out.String())
		}
	})
}

func TestPolishCmd(t *testing.T) {
	es := newTestStore(t)
	cfg := &models.Config{}

	t.Run("stdin text output", func(t *testing.T) {
		cmd := NewPolishCmd(es, cfg)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetIn(strings.NewReader("fix login bug in auth handler"))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		if len(out.String()) == 0 {
			t.Fatalf("expected non-empty polished prompt")
		}
	})

	t.Run("flags json output", func(t *testing.T) {
		cmd := NewPolishCmd(es, cfg)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--prompt", "implement rate limiter", "--json"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		var res map[string]interface{}
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			t.Fatalf("unmarshal json: %v, raw: %s", err, out.String())
		}
		if _, ok := res["polished_prompt"]; !ok {
			t.Fatalf("expected polished_prompt in json response, got %v", res)
		}
	})
}
