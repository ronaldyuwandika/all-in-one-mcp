package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestMaskCmd(t *testing.T) {
	t.Run("stdin masking", func(t *testing.T) {
		root := NewRoot()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetIn(strings.NewReader("api_key = AKIAIOSFODNN7EXAMPLEQQ"))
		root.SetArgs([]string{"mask"})

		if err := root.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		res := out.String()
		if strings.Contains(res, "AKIAIOSFODNN7EXAMPLEQQ") {
			t.Fatalf("expected secret to be masked, got: %s", res)
		}
		if !strings.Contains(res, "[REDACTED") {
			t.Fatalf("expected [REDACTED marker, got: %s", res)
		}
	})

	t.Run("argument masking", func(t *testing.T) {
		root := NewRoot()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs([]string{"mask", "token=ghp_123456789012345678901234567890123456"})

		if err := root.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}
		res := out.String()
		if strings.Contains(res, "ghp_123456789012345678901234567890123456") {
			t.Fatalf("expected token to be masked, got: %s", res)
		}
		if !strings.Contains(res, "[REDACTED") {
			t.Fatalf("expected [REDACTED marker, got: %s", res)
		}
	})
}
