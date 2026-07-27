package linkcontent

import (
	"net/url"
	"strings"
	"unicode"
)

func ExtractURLs(input string, max int) []string {
	seen := make(map[string]bool)
	var out []string
	for _, token := range strings.Fields(input) {
		token = strings.TrimFunc(token, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("<>[]{}()\"'`,;.?!", r) })
		u, err := url.Parse(token)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			continue
		}
		u.Fragment = ""
		normalized := u.String()
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}
