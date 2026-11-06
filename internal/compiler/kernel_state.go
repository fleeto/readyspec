package compiler

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	sysctlDecimal  = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
	moduleName     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	moduleUnsigned = regexp.MustCompile(`^[0-9]+$`)
	moduleRefcount = regexp.MustCompile(`^(?:-|[0-9]+|-[0-9]+)$`)
	moduleAddress  = regexp.MustCompile(`^0[xX][0-9a-fA-F]+$`)
	moduleFlags    = regexp.MustCompile(`^\([A-Za-z0-9_+\-]+\)$`)
)

func sysctlEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{}
	if text == "__RS_SYSCTL_MISSING__" {
		summary["exists"] = false
		return "FAIL", summary
	}
	if !strings.HasPrefix(text, "__RS_SYSCTL_VALUE__\n") {
		return "ERROR", summary
	}
	raw := strings.TrimPrefix(text, "__RS_SYSCTL_VALUE__\n")
	summary["exists"] = true
	summary["value_raw"] = raw
	value := strings.TrimSuffix(raw, "\n")
	if strings.ContainsAny(value, "\n\r\x00") {
		return "ERROR", summary
	}
	value = strings.Trim(value, " \t")
	summary["value"] = value
	if expected, ok := params["value"]; ok {
		if exact, ok := expected.(string); ok {
			if value == exact {
				return "PASS", summary
			}
			return "FAIL", summary
		}
		if !sysctlDecimal.MatchString(value) {
			return "ERROR", summary
		}
		return exactNumericMatch(expected, value, true), summary
	}
	if !sysctlDecimal.MatchString(value) {
		return "ERROR", summary
	}
	rules := []any{}
	for _, bound := range []struct{ name, operation string }{{"minimum", "ge"}, {"maximum", "le"}} {
		if threshold, ok := params[bound.name]; ok {
			rules = append(rules, map[string]any{bound.operation: threshold})
		}
	}
	if len(rules) == 0 {
		return "ERROR", summary
	}
	return exactNumericMatch(map[string]any{"and": rules}, value, true), summary
}

// /proc/modules has six required fields and optional taint flags. Kernels
// without CONFIG_MODULE_UNLOAD print '-' for both refcount and dependencies.
// Validate every row before treating an absent module as a reliable absence.
func moduleEvaluation(params map[string]any, text string) (string, map[string]any) {
	requested, ok := params["name"].(string)
	if !ok {
		return "ERROR", map[string]any{}
	}
	requested = strings.ReplaceAll(requested, "-", "_")
	summary := map[string]any{"name": requested}
	if !moduleName.MatchString(requested) {
		return "ERROR", summary
	}
	states := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 || len(fields) > 7 {
			return "ERROR", summary
		}
		if !moduleName.MatchString(fields[0]) || !moduleUnsigned.MatchString(fields[1]) || !moduleRefcount.MatchString(fields[2]) || !moduleAddress.MatchString(fields[5]) {
			return "ERROR", summary
		}
		if _, err := strconv.ParseUint(fields[1], 10, 64); err != nil {
			return "ERROR", summary
		}
		if fields[2] != "-" {
			if _, err := strconv.ParseInt(fields[2], 10, 64); err != nil {
				return "ERROR", summary
			}
		}
		if fields[3] != "-" {
			for _, dependency := range strings.Split(strings.TrimSuffix(fields[3], ","), ",") {
				if dependency != "[permanent]" && !moduleName.MatchString(dependency) {
					return "ERROR", summary
				}
			}
		}
		if fields[4] != "Live" && fields[4] != "Loading" && fields[4] != "Unloading" {
			return "ERROR", summary
		}
		if len(fields) == 7 && !moduleFlags.MatchString(fields[6]) {
			return "ERROR", summary
		}
		name := strings.ReplaceAll(fields[0], "-", "_")
		if _, duplicate := states[name]; duplicate {
			return "ERROR", summary
		}
		states[name] = fields[4]
	}
	state, present := states[requested]
	summary["module_count"] = len(states)
	summary["state"] = state
	if present && state != "Live" {
		return "ERROR", summary
	}
	summary["loaded"] = present
	expected, ok := params["loaded"].(bool)
	if !ok {
		return "ERROR", summary
	}
	if present == expected {
		return "PASS", summary
	}
	return "FAIL", summary
}
