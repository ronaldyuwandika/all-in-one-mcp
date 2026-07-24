package store

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRedactingHandlerMasksMessagesAndAttributes(t *testing.T) {
	var output bytes.Buffer
	handler := redactingHandler{next: slog.NewTextHandler(&output, nil)}
	secret := "ghp_abcdefghijklmnopqrstuvwxyz"
	record := slog.NewRecord(time.Unix(0, 0), slog.LevelError, "failed with "+secret, 0)
	record.AddAttrs(slog.String("detail", "Authorization: Bearer abcdefghijklmnop"))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if strings.Contains(got, secret) || strings.Contains(got, "abcdefghijklmnop") {
		t.Fatalf("log leaked secret: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("log did not contain redaction marker: %s", got)
	}
}
