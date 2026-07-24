package secretdetect

import (
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Redact deterministically replaces high-confidence secrets with [REDACTED].
func Redact(text string) Result {
	return RedactWithConfig(text, DefaultConfig())
}

func RedactWithConfig(text string, config Config) Result {
	config = normalizeConfig(config)
	matches := detect(text, config)
	if len(matches) == 0 {
		return Result{Text: text}
	}

	var out strings.Builder
	cursor := 0
	findings := make([]Finding, 0, len(matches))
	for _, m := range matches {
		if m.start < cursor {
			// A broader multi-line secret (for example a PEM block assigned to
			// PRIVATE_KEY) may extend beyond an earlier assignment match.
			if m.end > cursor {
				out.WriteString(m.replacement)
				findings = append(findings, newFinding(m))
				cursor = m.end
			}
			continue
		}
		out.WriteString(text[cursor:m.start])
		out.WriteString(m.replacement)
		findings = append(findings, newFinding(m))
		cursor = m.end
	}
	out.WriteString(text[cursor:])
	return Result{Text: out.String(), Findings: findings}
}

func newFinding(m match) Finding {
	sum := sha256.Sum256([]byte(m.kind + "\x00" + m.value))
	return Finding{
		Type: m.kind, Offset: m.start, End: m.end, Confidence: m.confidence,
		Fingerprint: fmt.Sprintf("%x", sum[:12]),
	}
}

// DetectValues exists for credential-vault's encrypted scan-and-restore workflow.
// Callers must treat all returned values as credentials; Redact findings never
// contain these values.
func DetectValues(text string) map[string]string {
	out := make(map[string]string)
	counters := make(map[string]int)
	for _, m := range detect(text, DefaultConfig()) {
		name := m.name
		if name == "" {
			name = fmt.Sprintf("%s_%d", m.kind, counters[m.kind])
			counters[m.kind]++
		}
		out[name] = m.value
	}
	return out
}

func detect(text string, config Config) []match {
	var matches []match
	addRegexpMatches := func(kind string, confidence float64, indices [][]int, replacement func([]int) string, valueGroup int, nameGroup int) {
		for _, idx := range indices {
			start, end := idx[0], idx[1]
			value := text[start:end]
			if valueGroup > 0 && idx[valueGroup*2] >= 0 {
				value = text[idx[valueGroup*2]:idx[valueGroup*2+1]]
			}
			name := ""
			if nameGroup > 0 && idx[nameGroup*2] >= 0 {
				name = text[idx[nameGroup*2]:idx[nameGroup*2+1]]
			}
			matches = append(matches, match{
				start: start, end: end, kind: kind, replacement: replacement(idx),
				name: name, value: value, confidence: confidence,
			})
		}
	}

	addRegexpMatches("private_key", 1, privateKeyPattern.FindAllStringSubmatchIndex(text, -1),
		func([]int) string { return config.Replacement }, 0, 0)
	addRegexpMatches("authorization", 1, authorizationPattern.FindAllStringSubmatchIndex(text, -1),
		func(idx []int) string { return text[idx[2]:idx[3]] + config.Replacement }, 0, 0)
	addRegexpMatches("connection_string", 1, connectionPattern.FindAllStringSubmatchIndex(text, -1),
		func(idx []int) string {
			return text[idx[2]:idx[3]] + "://" + text[idx[4]:idx[5]] + ":" + config.Replacement + "@" + text[idx[8]:idx[9]]
		}, 3, 0)
	addRegexpMatches("assignment", 0.99, assignmentPattern.FindAllStringSubmatchIndex(text, -1),
		func(idx []int) string { return text[idx[2]:idx[3]] + "=" + config.Replacement }, 3, 1)
	addRegexpMatches("provider_token", 1, providerPattern.FindAllStringSubmatchIndex(text, -1),
		func([]int) string { return config.Replacement }, 0, 0)
	addRegexpMatches("jwt", 0.99, jwtPattern.FindAllStringSubmatchIndex(text, -1),
		func([]int) string { return config.Replacement }, 0, 0)
	addRegexpMatches("cli_argument", 0.99, cliArgumentPattern.FindAllStringSubmatchIndex(text, -1),
		func(idx []int) string { return text[idx[2]:idx[3]] + config.Replacement }, 2, 0)
	addRegexpMatches("structured_secret", 0.99, yamlSecretPattern.FindAllStringSubmatchIndex(text, -1),
		func(idx []int) string { return text[idx[2]:idx[3]] + config.Replacement }, 2, 0)
	addRegexpMatches("terraform_variable", 0.98, terraformPattern.FindAllStringSubmatchIndex(text, -1),
		func(idx []int) string { return text[idx[2]:idx[3]] + config.Replacement + text[idx[4]:idx[5]] }, 0, 0)

	if config.DetectHighEntropy {
		for _, idx := range entropyCandidate.FindAllStringIndex(text, -1) {
			value := text[idx[0]:idx[1]]
			if !looksHighEntropy(value, config) {
				continue
			}
			matches = append(matches, match{
				start: idx[0], end: idx[1], kind: "high_entropy",
				replacement: config.Replacement, value: value, confidence: 0.8,
			})
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].start == matches[j].start {
			return matches[i].end > matches[j].end
		}
		return matches[i].start < matches[j].start
	})
	return matches
}

func looksHighEntropy(value string, config Config) bool {
	if len(value) < config.MinEntropyLength || hexPattern.MatchString(value) || uuidPattern.MatchString(value) {
		return false
	}
	var lower, upper, digit, symbol bool
	counts := make(map[rune]float64)
	runes := []rune(value)
	for _, r := range runes {
		counts[r]++
		lower = lower || unicode.IsLower(r)
		upper = upper || unicode.IsUpper(r)
		digit = digit || unicode.IsDigit(r)
		symbol = symbol || strings.ContainsRune("_-/+=", r)
	}
	classes := 0
	for _, present := range []bool{lower, upper, digit, symbol} {
		if present {
			classes++
		}
	}
	if classes < 3 {
		return false
	}
	var entropy float64
	for _, count := range counts {
		p := count / float64(len(runes))
		entropy -= p * math.Log2(p)
	}
	return entropy >= config.MinEntropy
}
