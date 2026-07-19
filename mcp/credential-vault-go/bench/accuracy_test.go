package bench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/vault"
)

func TestDetectionAccuracy(t *testing.T) {
	positives := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		positives = append(positives, fmt.Sprintf("SERVICE_%03d_API_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz%03d", i, i))
	}
	benign := make([]string, 0, 1000)
	for i := 0; i < 1000; i++ {
		benign = append(benign, fmt.Sprintf("SERVICE_%03d_PORT=%d", i, 8000+i))
	}
	tp, fn, fp := 0, 0, 0
	for _, x := range positives {
		if len(vault.Detect(x)) > 0 {
			tp++
		} else {
			fn++
			t.Logf("false negative: %s", x)
		}
	}
	for _, x := range benign {
		if len(vault.Detect(x)) > 0 {
			fp++
		}
	}
	recall := float64(tp) / float64(tp+fn)
	precision := float64(tp) / float64(tp+fp)
	if recall < 0.98 || precision < 0.99 {
		t.Fatalf("precision=%.2f recall=%.2f", precision, recall)
	}
	for _, x := range positives {
		masked := vault.MaskText(x)
		for _, secret := range vault.Detect(x) {
			if strings.Contains(masked, secret) {
				t.Fatalf("secret remains after masking: %s", secret)
			}
		}
	}
}
