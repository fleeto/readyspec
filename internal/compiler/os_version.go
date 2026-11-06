package compiler

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	osReleaseKey       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	osReleaseID        = regexp.MustCompile(`^[a-z0-9._-]+$`)
	osReleaseVersionID = regexp.MustCompile(`^[A-Za-z0-9._~^+-]+$`)
	kernelRelease      = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+){0,3})([-+_][A-Za-z0-9._+~:-]*)?$`)
)

// Parse only data assignments, never source os-release in a shell. Shell
// quoting differs from Go literals: e.g. \$ is valid, \x2e is not a hex escape.
func osReleaseValue(raw string) (string, bool) {
	var value strings.Builder
	tailOK := func(tail string) bool {
		trimmed := strings.TrimLeft(tail, " \t")
		return trimmed == "" || (len(trimmed) < len(tail) && strings.HasPrefix(trimmed, "#"))
	}
	if raw == "" {
		return "", true
	}
	quote := byte(0)
	offset := 0
	if raw[0] == '\'' || raw[0] == '"' {
		quote = raw[0]
		offset = 1
	}
	for offset < len(raw) {
		character := raw[offset]
		if character == 0 || character == '\r' {
			return "", false
		}
		if quote != 0 && character == quote {
			return value.String(), tailOK(raw[offset+1:])
		}
		if quote == 0 && (character == ' ' || character == '\t') {
			return value.String(), tailOK(raw[offset:])
		}
		if character == '\\' && quote != '\'' {
			if offset+1 == len(raw) {
				return "", false
			}
			next := raw[offset+1]
			if next == 0 || next == '\r' {
				return "", false
			}
			if quote == '"' && next != '\\' && next != '"' && next != '$' && next != '`' {
				value.WriteByte('\\')
			}
			value.WriteByte(next)
			offset += 2
			continue
		}
		if quote != '\'' && (character == '$' || character == '`') {
			return "", false
		}
		if quote == 0 && strings.ContainsRune("'\";|&<>()", rune(character)) {
			return "", false
		}
		value.WriteByte(character)
		offset++
	}
	return value.String(), quote == 0
}

func osReleaseEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{}
	fields, rawFields := map[string]string{}, map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimLeft(strings.TrimSuffix(line, "\r"), " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok || !osReleaseKey.MatchString(key) {
			return "ERROR", summary
		}
		if _, duplicate := fields[key]; duplicate {
			return "ERROR", summary
		}
		value, ok := osReleaseValue(raw)
		if !ok {
			return "ERROR", summary
		}
		fields[key] = value
		if key == "ID" || key == "VERSION_ID" {
			rawFields[key] = raw
		}
	}
	if !osReleaseID.MatchString(fields["ID"]) || !osReleaseVersionID.MatchString(fields["VERSION_ID"]) {
		return "ERROR", summary
	}
	summary["os_id"] = fields["ID"]
	summary["version_id"] = fields["VERSION_ID"]
	summary["raw_fields"] = rawFields
	allowed, ok := params["allowed_ids"].([]any)
	if !ok || len(allowed) == 0 {
		return "ERROR", summary
	}
	matched := false
	for _, id := range allowed {
		if id == fields["ID"] {
			matched = true
		}
	}
	if !matched {
		return "FAIL", summary
	}
	return numericVersionBounds(fields["VERSION_ID"], params), summary
}

func numericVersionBounds(version string, params map[string]any) string {
	mismatch := false
	for _, bound := range []struct {
		key     string
		minimum bool
	}{{"minimum_version", true}, {"maximum_version", false}} {
		if value, exists := params[bound.key]; exists {
			expected, ok := value.(string)
			if !ok {
				return "ERROR"
			}
			comparison, valid := compareVersions(version, expected)
			if !valid {
				return "ERROR"
			}
			if (bound.minimum && comparison < 0) || (!bound.minimum && comparison > 0) {
				mismatch = true
			}
		}
	}
	if mismatch {
		return "FAIL"
	}
	return "PASS"
}

func kernelEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{"kernel_raw": text, "normalized_version": "", "release_suffix": ""}
	parts := kernelRelease.FindStringSubmatch(strings.TrimSuffix(text, "\n"))
	if len(parts) != 3 {
		return "ERROR", summary
	}
	components := strings.Split(parts[1], ".")
	for index, component := range components {
		number, err := strconv.ParseUint(component, 10, 64)
		if err != nil {
			return "ERROR", summary
		}
		components[index] = strconv.FormatUint(number, 10)
	}
	for len(components) < 3 {
		components = append(components, "0")
	}
	normalized := strings.Join(components, ".")
	summary["normalized_version"] = normalized
	summary["release_suffix"] = parts[2]
	return numericVersionBounds(normalized, params), summary
}
