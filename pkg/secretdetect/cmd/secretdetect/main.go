// Command secretdetect is a local JSON filter used by migration tooling.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ronaldyuwandika/all-in-one-mcp/pkg/secretdetect"
)

func main() {
	decoder := json.NewDecoder(os.Stdin)
	decoder.UseNumber()
	var input any
	if err := decoder.Decode(&input); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "invalid JSON input")
		os.Exit(2)
	}
	output := secretdetect.RedactValue(input, secretdetect.DefaultConfig())
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "encode sanitized JSON")
		os.Exit(1)
	}
}
