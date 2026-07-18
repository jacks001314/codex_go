package safety

import "regexp"

var (
	openAIKeyRegex        = regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)
	awsAccessKeyIDRegex   = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	bearerTokenRegex      = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-]{16,}\b`)
	secretAssignmentRegex = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password)\b(\s*[:=]\s*)(["']?)[^\s"']{8,}`)
)

func RedactSecrets(input string) string {
	redacted := openAIKeyRegex.ReplaceAllString(input, "[REDACTED_SECRET]")
	redacted = awsAccessKeyIDRegex.ReplaceAllString(redacted, "[REDACTED_SECRET]")
	redacted = bearerTokenRegex.ReplaceAllString(redacted, "Bearer [REDACTED_SECRET]")
	redacted = secretAssignmentRegex.ReplaceAllString(redacted, "$1$2$3[REDACTED_SECRET]")
	return redacted
}
