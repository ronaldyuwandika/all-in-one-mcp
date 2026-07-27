package linkcontent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExtractURLs(t *testing.T) {
	got := ExtractURLs("See https://example.com/a#frag, https://example.com/a and https://other.test/x.", 5)
	if len(got) != 2 || got[0] != "https://example.com/a" || got[1] != "https://other.test/x" {
		t.Fatalf("unexpected URLs: %#v", got)
	}
}

func TestValidateURL(t *testing.T) {
	for _, raw := range []string{"ftp://example.com", "https://user:pass@example.com", "http://127.0.0.1"} {
		if _, err := ValidateURL(raw); err == nil {
			t.Fatalf("expected URL rejection for %q", raw)
		}
	}
}

func TestExtractText(t *testing.T) {
	title, text, err := ExtractText("text/html", []byte("<html><head><title>Task</title><script>bad()</script></head><body>Hello <b>world</b></body></html>"))
	if err != nil || title != "Task" || text != "Hello\nworld" {
		t.Fatalf("unexpected extraction: %q %q %v", title, text, err)
	}
}

type fakeFetcher struct{}

func (fakeFetcher) Fetch(context.Context, string) (*FetchResult, error) {
	return &FetchResult{ContentType: "text/plain", Body: []byte("Task: fix bug\nAcceptance: tests pass")}, nil
}

type fakeSummarizer struct{}

func (fakeSummarizer) Summarize(context.Context, string, string, string, string, int) (Source, error) {
	return Source{Status: StatusSummarized, Summary: "Fix bug", Instructions: []string{"Fix bug"}, AcceptanceCriteria: []string{"Tests pass"}, Constraints: []string{}}, nil
}

func TestServiceProcess(t *testing.T) {
	service := NewService(DefaultConfig(), fakeFetcher{}, fakeSummarizer{})
	got, err := service.Process(context.Background(), "Task https://example.com/bug")
	if err != nil || len(got) != 1 || got[0].Status != StatusSummarized || got[0].ContentHash == "" {
		t.Fatalf("unexpected result: %#v %v", got, err)
	}
}

func TestProvidedRequiresMatchingSummary(t *testing.T) {
	service := NewService(DefaultConfig(), nil, nil)
	got, warnings, err := service.ProcessProvided("Task https://example.com/bug", nil)
	if err != nil || len(got) != 1 || got[0].Warning != "link_summary_required" || len(warnings) != 1 {
		t.Fatalf("unexpected result: %#v %#v %v", got, warnings, err)
	}
}

func TestJSONSummarizerRejectsInvalidResponse(t *testing.T) {
	s := JSONSummarizer{Callback: func(context.Context, string) (string, error) { return "not-json", nil }}
	_, err := s.Summarize(context.Background(), "https://example.com", SourceTypeWebPage, "", "text", 100)
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("expected invalid JSON error: %v", err)
	}
}

func TestSafeSourceURLRemovesSensitiveComponents(t *testing.T) {
	got := SafeSourceURL("https://user:pass@example.com/task?id=secret#fragment")
	if got != "https://example.com/task" {
		t.Fatalf("unexpected safe URL: %q", got)
	}
}

func TestCacheIsBoundedAndDeepCopies(t *testing.T) {
	cache := NewCache(15, 1)
	source := Source{Summary: "one", Instructions: []string{"original"}}
	cache.Put("one", source)
	source.Instructions[0] = "mutated"
	got, ok := cache.Get("one")
	if !ok || got.Instructions[0] != "original" {
		t.Fatalf("cache did not deep copy: %#v", got)
	}
	cache.Put("two", Source{Summary: "two"})
	if _, ok := cache.Get("one"); ok {
		t.Fatal("expected oldest cache entry eviction")
	}
}

func TestValidateSummaryRejectsEmptyOrOversized(t *testing.T) {
	if validateSummary(Source{}, 100) == nil {
		t.Fatal("expected empty summary rejection")
	}
	if validateSummary(Source{Summary: strings.Repeat("x", 101)}, 100) == nil {
		t.Fatal("expected aggregate budget rejection")
	}
}
