package secretdetect

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRedactCoverage(t *testing.T) {
	slackBotToken := "xoxb-" + "1234567890" + "-" + "abcdefghijklmnop"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"github", "token ghp_abcdefghijklmnopqrstuvwxyz", "token [REDACTED]"},
		{"openai", "sk-proj-abcdefghijklmnopqrstuvwxyz", "[REDACTED]"},
		{"google", "AIzaabcdefghijklmnopqrstuvwxyz1234", "[REDACTED]"},
		{"slack", slackBotToken, "[REDACTED]"},
		{"stripe", "sk_test_abcdefghijklmnopqrstuvwxyz", "[REDACTED]"},
		{"assignment", "DATABASE_PASSWORD='correct-horse'", "DATABASE_PASSWORD=[REDACTED]"},
		{"authorization", "Authorization: Bearer abcdefghijklmnop", "Authorization: Bearer [REDACTED]"},
		{"postgres", "postgres://user:hunter2@db.local/app", "postgres://user:[REDACTED]@db.local/app"},
		{"unicode", "🔐 TOKEN=秘密credential", "🔐 TOKEN=[REDACTED]"},
		{"private key", "x\n-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\ny", "x\n[REDACTED]\ny"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", "[REDACTED]"},
		{"cli", "deploy --token abcdefghijklmnop", "deploy --token [REDACTED]"},
		{"yaml", "password: supersecretvalue", "password: [REDACTED]"},
		{"terraform", "variable \"db_password\" { default = \"supersecretvalue\" }", "variable \"db_password\" { default = \"[REDACTED]\" }"},
		{"entropy", "blob Qw7Zp9Lm2Nx8Vr4Ks6Ht1Bc5Df0Gj3YuEaIi", "blob [REDACTED]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.in)
			if got.Text != tc.want {
				t.Fatalf("Redact() = %q, want %q", got.Text, tc.want)
			}
			for _, finding := range got.Findings {
				if strings.Contains(strings.ToLower(finding.Type), "abc") {
					t.Fatalf("finding leaked value: %#v", finding)
				}
				if finding.End <= finding.Offset || finding.Confidence <= 0 || finding.Confidence > 1 {
					t.Fatalf("invalid finding metadata: %#v", finding)
				}
				if len(finding.Fingerprint) != 24 || strings.Contains(finding.Fingerprint, "correct-horse") {
					t.Fatalf("invalid fingerprint: %#v", finding)
				}
			}
		})
	}
}

func TestAssignedPrivateKeyDoesNotLeakTail(t *testing.T) {
	in := "PRIVATE_KEY=-----BEGIN PRIVATE KEY-----\nsecret-body\n-----END PRIVATE KEY-----"
	got := Redact(in)
	if strings.Contains(got.Text, "secret-body") || strings.Contains(got.Text, "PRIVATE KEY-----") {
		t.Fatalf("private key tail leaked: %q", got.Text)
	}
}

func TestRedactDeterministicAndMultiple(t *testing.T) {
	in := "API_TOKEN=abcdefgh\nAuthorization: Basic dXNlcjpwYXNz"
	a, b := Redact(in), Redact(in)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("non-deterministic results: %#v %#v", a, b)
	}
	if len(a.Findings) != 2 || strings.Contains(a.Text, "abcdefgh") || strings.Contains(a.Text, "dXNlcjpwYXNz") {
		t.Fatalf("unexpected result: %#v", a)
	}
}

func TestFalsePositiveRegressions(t *testing.T) {
	in := "commit 0123456789abcdef0123456789abcdef01234567 uuid 123e4567-e89b-12d3-a456-426614174000 checksum deadbeef"
	got := Redact(in)
	if got.Text != in || len(got.Findings) != 0 {
		t.Fatalf("false positive: %#v", got)
	}
}

func TestConfigurableReplacementAndEntropy(t *testing.T) {
	entropy := "Qw7Zp9Lm2Nx8Vr4Ks6Ht1Bc5Df0Gj3YuEaIi"
	got := RedactWithConfig("value "+entropy, Config{
		Replacement: "[MASKED]", DetectHighEntropy: false,
	})
	if got.Text != "value "+entropy {
		t.Fatalf("entropy detection was not disabled: %q", got.Text)
	}
	got = RedactWithConfig("TOKEN=abcdefghijklmnop", Config{Replacement: "<secret>"})
	if got.Text != "TOKEN=<secret>" {
		t.Fatalf("custom replacement not applied: %q", got.Text)
	}
}

func TestFindingsNeverContainSecretValues(t *testing.T) {
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	got := Redact(secret + " and " + secret)
	raw := fmt.Sprintf("%#v", got.Findings)
	if strings.Contains(raw, secret) {
		t.Fatalf("finding leaked secret: %s", raw)
	}
	if len(got.Findings) != 2 || got.Findings[0].Fingerprint != got.Findings[1].Fingerprint {
		t.Fatalf("fingerprint is not stable: %#v", got.Findings)
	}
}

func TestProviderAndStructuredRegressionCorpus(t *testing.T) {
	stripeRestrictedLiveKey := "rk_" + "live_" + "abcdefghijklmnopqrstuvwxyz"
	secrets := []string{
		"AKIAABCDEFGHIJKLMNOP",
		"ASIAABCDEFGHIJKLMNOP",
		"gho_abcdefghijklmnopqrstuvwxyz",
		"ghu_abcdefghijklmnopqrstuvwxyz",
		"ghs_abcdefghijklmnopqrstuvwxyz",
		"ghr_abcdefghijklmnopqrstuvwxyz",
		"github_pat_abcdefghijklmnopqrstuvwxyz",
		"glpat-abcdefghijklmnopqrstuvwxyz",
		"sk-abcdefghijklmnopqrstuvwxyz",
		stripeRestrictedLiveKey,
		"rk_test_abcdefghijklmnopqrstuvwxyz",
		"xoxp-1234567890-abcdefghijklmnop",
		"xoxa-1234567890-abcdefghijklmnop",
		"xoxr-1234567890-abcdefghijklmnop",
	}
	for _, secret := range secrets {
		t.Run(secret[:4], func(t *testing.T) {
			got := Redact("before " + secret + " after")
			if strings.Contains(got.Text, secret) || len(got.Findings) == 0 {
				t.Fatalf("secret not detected: %q => %#v", secret, got)
			}
		})
	}

	connectionStrings := []string{
		"postgresql://user:password@host/db",
		"mysql://user:password@host/db",
		"mongodb://user:password@host/db",
		"mongodb+srv://user:password@host/db",
		"redis://user:password@host/0",
	}
	for _, connectionString := range connectionStrings {
		got := Redact(connectionString)
		if strings.Contains(got.Text, ":password@") {
			t.Fatalf("connection password leaked: %q", got.Text)
		}
	}
}
