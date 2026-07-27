package linkcontent

import (
	"bytes"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

func ExtractText(contentType string, body []byte) (title, text string, err error) {
	if strings.EqualFold(contentType, "text/plain") {
		return "", strings.TrimSpace(string(body)), nil
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	var titleBuilder, textBuilder strings.Builder
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, hidden bool) {
		if n == nil {
			return
		}
		isHidden := hidden || n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript" || n.Data == "svg")
		if n.Type == html.ElementNode && n.Data == "title" {
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.TextNode {
					titleBuilder.WriteString(child.Data)
				}
			}
		}
		if n.Type == html.TextNode && !isHidden && !isInsideTitle(n) {
			value := strings.TrimFunc(n.Data, unicode.IsSpace)
			if value != "" {
				textBuilder.WriteString(value)
				textBuilder.WriteByte('\n')
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, isHidden)
		}
	}
	walk(doc, false)
	return strings.TrimSpace(titleBuilder.String()), strings.TrimSpace(textBuilder.String()), nil
}

func isInsideTitle(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Data == "title" {
			return true
		}
	}
	return false
}
