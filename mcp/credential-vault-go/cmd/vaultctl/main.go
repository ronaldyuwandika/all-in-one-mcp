package main

import (
	"fmt"
	"os"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault-go/internal/cli"
)

func main() {
	cmd := cli.NewRoot()
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
