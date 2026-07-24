# secretdetect

`secretdetect` is the repository's shared, offline Go package for deterministic secret detection and redaction.

```go
result := secretdetect.Redact(text)
fmt.Println(result.Text)
```

`Result.Findings` contains metadata only: secret type, byte range, confidence, and a stable truncated SHA-256 fingerprint. It never contains the detected credential. `RedactWithConfig` supports a custom replacement marker and optional high-entropy detection settings.

Coverage includes credential assignments, common API/cloud/GitHub/GitLab/Slack/Stripe tokens, authorization headers, JWTs, credential-bearing database URLs, PEM private keys, CLI credential arguments, YAML/Kubernetes-style secret fields, Terraform credential variables, and conservative unknown high-entropy candidates.

Detection is best-effort. The entropy detector requires a long, diverse character sequence and excludes hexadecimal hashes and UUIDs to reduce false positives. Applications must still avoid accepting intentional secrets in logs, prompts, or memory.
