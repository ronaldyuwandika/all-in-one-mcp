package main

import (
	"log"

	vaultmcp "github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault/internal/mcp"
	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/credential-vault/internal/vault"
)

func main() {
	v, err := vault.Default()
	if err != nil {
		log.Fatal(err)
	}
	if err = vaultmcp.Serve(v); err != nil {
		log.Fatal(err)
	}
}
