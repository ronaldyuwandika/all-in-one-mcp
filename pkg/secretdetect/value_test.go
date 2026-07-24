package secretdetect

import (
	"strings"
	"testing"
)

func TestRedactValueNestedMetadata(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	input := map[string]any{
		"nested": []any{
			map[string]any{"token": secret},
			"Authorization: Bearer abcdefghijklmnop",
		},
	}
	got := RedactValue(input, DefaultConfig())
	text := got.(map[string]any)["nested"].([]any)
	if strings.Contains(text[0].(map[string]any)["token"].(string), secret) ||
		strings.Contains(text[1].(string), "abcdefghijklmnop") {
		t.Fatalf("nested value leaked: %#v", got)
	}
}
