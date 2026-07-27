package main

import (
	"encoding/json"

	"github.com/ronaldyuwandika/all-in-one-mcp/mcp/reasoning-memory/internal/linkcontent"
)

func renderLinkedSources(sources []linkcontent.Source) (string, error) {
	data, err := json.Marshal(sources)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
