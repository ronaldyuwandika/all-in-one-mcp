package secretdetect

import "regexp"

var (
	privateKeyPattern    = regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY-----`)
	authorizationPattern = regexp.MustCompile(`(?im)(Authorization\s*:\s*(?:Bearer|Basic)\s+)[^\s,;]+`)
	connectionPattern    = regexp.MustCompile(`(?i)\b(postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis)://([^:@/\s]+):([^@/\s]+)@([^\s]+)`)
	assignmentPattern    = regexp.MustCompile(`(?i)\b((?:[A-Z0-9_]+_)?(?:TOKEN|PASSWORD|PASSWD|SECRET|API_KEY|PRIVATE_KEY|ACCESS_KEY)(?:_[A-Z0-9_]+)*)\s*=\s*(["']?)([^\s"',]+)(?:["']?)`)
	providerPattern      = regexp.MustCompile(`\b(?:AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|github_pat_[A-Za-z0-9_]{10,}|gh[pousr]_[A-Za-z0-9_]{10,}|glpat-[A-Za-z0-9_-]{10,}|sk-proj-[A-Za-z0-9_-]{16,}|sk-[A-Za-z0-9_-]{16,}|(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{10,}|AIza[A-Za-z0-9_-]{20,}|xox[bparo]-[A-Za-z0-9-]{10,})\b`)
	jwtPattern           = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	cliArgumentPattern   = regexp.MustCompile(`(?i)(--(?:api[-_]?key|token|password|passwd|secret|access[-_]?key)\s+)([^\s]+)`)
	yamlSecretPattern    = regexp.MustCompile(`(?im)^(\s*(?:password|passwd|token|secret|api[_-]?key|access[_-]?key|private[_-]?key)\s*:\s*)([^\s#]{4,})`)
	terraformPattern     = regexp.MustCompile(`(?is)(variable\s+"[^"]*(?:password|token|secret|key)[^"]*"\s*\{[^{}]*?\bdefault\s*=\s*")[^"]+(")`)
	entropyCandidate     = regexp.MustCompile(`\b[A-Za-z0-9][A-Za-z0-9+/=_-]{31,}\b`)
	uuidPattern          = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	hexPattern           = regexp.MustCompile(`(?i)^[0-9a-f]+$`)
)
