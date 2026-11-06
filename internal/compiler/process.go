package compiler

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var processRow = regexp.MustCompile(`^[ \t]*([0-9]+)[ \t]+(.*)$`)

// PID makes a successfully read empty match distinguishable from an empty or
// damaged process table. Validate all rows before counting, but publish only
// the requested name and aggregate counts, never unrelated names or argv.
func processEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{"redacted": true, "matching_mode": "exact_comm"}
	invalid := func() (string, map[string]any) { summary["error"] = "INVALID_PROCESS_TABLE"; return "ERROR", summary }
	if text == "" || !utf8.ValidString(text) || strings.ContainsAny(text, "\x00\r") {
		return invalid()
	}
	name, ok := params["name"].(string)
	if !ok || name == "" {
		return invalid()
	}
	seen := map[uint64]bool{}
	count := 0
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		fields := processRow.FindStringSubmatch(line)
		if len(fields) != 3 {
			return invalid()
		}
		pid, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil || pid == 0 || seen[pid] {
			return invalid()
		}
		seen[pid] = true
		for _, character := range fields[2] {
			if character < 32 || character == 127 {
				return invalid()
			}
		}
		if fields[2] == name {
			count++
		}
	}
	summary["name"] = name
	summary["matched_count"] = count
	summary["process_records"] = len(seen)
	rules := []any{map[string]any{"ge": params["minimum_count"]}}
	if maximum, exists := params["maximum_count"]; exists {
		rules = append(rules, map[string]any{"le": maximum})
	}
	return exactNumericMatch(map[string]any{"and": rules}, count, false), summary
}
