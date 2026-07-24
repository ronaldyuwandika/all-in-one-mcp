package security

import "testing"

func TestConfigureReplacement(t *testing.T) {
	Configure("<masked>")
	t.Cleanup(func() { Configure("[REDACTED]") })
	if got := Text("TOKEN=abcdefghijklmnop"); got != "TOKEN=<masked>" {
		t.Fatalf("configured replacement not used: %q", got)
	}
}
