package compiler

import (
	"strings"
)

// uptimeEvaluation validates the complete /proc/uptime record before trusting
// the selected field. The second column is idle time, not part of the
// assertion, but a malformed record must not be treated as a scalar.
func uptimeEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{}
	invalid := func() (string, map[string]any) {
		summary["error"] = "INVALID_UPTIME"
		return "ERROR", summary
	}
	line := strings.TrimSuffix(text, "\n")
	if strings.ContainsAny(line, "\n\r\x00") {
		return invalid()
	}
	for _, character := range line {
		if character != ' ' && character != '\t' && (character < 33 || character > 126) {
			return invalid()
		}
	}
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return invalid()
	}
	for _, value := range fields {
		if !loadDecimal.MatchString(value) {
			return invalid()
		}
	}
	minimum, ok := params["minimum_seconds"]
	if !ok {
		return invalid()
	}
	summary["uptime_seconds"] = fields[0]
	summary["idle_seconds"] = fields[1]
	summary["minimum_seconds"] = minimum
	matchers := []any{map[string]any{"ge": minimum}}
	if maximum, exists := params["maximum_seconds"]; exists {
		summary["maximum_seconds"] = maximum
		matchers = append(matchers, map[string]any{"le": maximum})
	}
	status := exactNumericMatch(map[string]any{"and": matchers}, fields[0], false)
	return status, summary
}
