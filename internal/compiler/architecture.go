package compiler

import (
	"regexp"
	"strings"
)

var architectureToken = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// Preserve stdout before normalization. Shell command substitution would lose
// trailing blank lines and NUL bytes, potentially hiding malformed evidence.
func architectureEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{"uname_raw": text, "normalized_architecture": ""}
	value := strings.TrimSuffix(text, "\n")
	if !architectureToken.MatchString(value) {
		summary["error"] = "INVALID_UNAME_OUTPUT"
		return "ERROR", summary
	}
	normalized := value
	switch value {
	case "amd64":
		normalized = "x86_64"
	case "arm64":
		normalized = "aarch64"
	}
	summary["normalized_architecture"] = normalized
	allowed, ok := params["allowed_arch"].([]any)
	if !ok || len(allowed) == 0 {
		return "ERROR", summary
	}
	matched := false
	for _, candidate := range allowed {
		architecture, ok := candidate.(string)
		if !ok {
			return "ERROR", summary
		}
		if architecture == normalized {
			matched = true
		}
	}
	if matched {
		return "PASS", summary
	}
	return "FAIL", summary
}
