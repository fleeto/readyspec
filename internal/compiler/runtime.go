package compiler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

func runtimeCommand(values map[string]plan.ResolvedParam) string {
	kind := fmt.Sprint(values["runtime"].Value)
	name, flag := "python3", "--version"
	if kind == "node" {
		name = "node"
	}
	if kind == "java" {
		name, flag = "java", "-version"
	}
	resolve := `p=$(command -v ` + shellQuote(name) + `) || { printf '__RS_RUNTIME_MISSING__\n'; exit 0; }; `
	if path, ok := values["path"]; ok {
		resolve = `p=` + shellQuote(path.Value.(string)) + `; `
	}
	return resolve + `out=$(LC_ALL=C stat -L -c %F -- "$p" 2>&1); rc=$?; if [ "$rc" -ne 0 ]; then case "$out" in *'No such file or directory'*|*'Not a directory'*) printf '__RS_RUNTIME_MISSING__\n'; exit 0;; *) exit 2;; esac; fi; case "$out" in 'regular file'|'regular empty file') :;; *) exit 2;; esac; [ -x "$p" ] || exit 2; printf '__RS_RUNTIME_PATH__\n%s\n__RS_RUNTIME_VERSION__\n' "$p"; "$p" ` + flag + ` 2>&1; rc=$?; printf '\n__RS_RUNTIME_RC__\n%s\n' "$rc"`
}

var (
	pythonVersionLine    = regexp.MustCompile(`^Python ([0-9]+\.[0-9]+\.[0-9]+(?:(?:a|b|rc)[0-9]+)?\+?)$`)
	nodeVersionLine      = regexp.MustCompile(`^v([0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9][A-Za-z0-9._+-]*)?)$`)
	javaVersionLine      = regexp.MustCompile(`^(?:openjdk|java)(?: version)? (?:"([^"\r\n]+)"|([0-9][^ \t"]*))(?:[ \t]+[^"\r\n]*)?$`)
	runtimeNumericPrefix = regexp.MustCompile(`^([0-9]+(?:[._][0-9]+)*)(.*)$`)
	javaVersionToken     = regexp.MustCompile(`^[0-9]+(?:[._][0-9]+)*(?:[-+][A-Za-z0-9][A-Za-z0-9._+-]*)?$`)
)

func runtimeEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{"runtime": params["runtime"]}
	invalid := func(reason string) (string, map[string]any) { summary["error"] = reason; return "ERROR", summary }
	if text == "__RS_RUNTIME_MISSING__\n" {
		summary["exists"] = false
		return "FAIL", summary
	}
	if !strings.HasPrefix(text, "__RS_RUNTIME_PATH__\n") {
		return invalid("INVALID_RUNTIME_OBSERVATION")
	}
	pathParts := strings.Split(strings.TrimPrefix(text, "__RS_RUNTIME_PATH__\n"), "\n__RS_RUNTIME_VERSION__\n")
	if len(pathParts) != 2 || !strings.HasPrefix(pathParts[0], "/") || strings.ContainsAny(pathParts[0], "\r\n\x00") {
		return invalid("INVALID_RUNTIME_OBSERVATION")
	}
	body := strings.Split(pathParts[1], "\n__RS_RUNTIME_RC__\n")
	if len(body) != 2 {
		return invalid("INVALID_RUNTIME_OBSERVATION")
	}
	rcText := strings.TrimSuffix(body[1], "\n")
	rc, err := strconv.Atoi(rcText)
	if err != nil || rc < 0 || rc > 255 {
		return invalid("INVALID_RUNTIME_OBSERVATION")
	}
	raw := body[0]
	summary["exists"] = true
	summary["path"] = pathParts[0]
	summary["version_raw"] = raw
	summary["query_exit_status"] = rc
	if rc != 0 {
		return invalid("RUNTIME_QUERY_FAILED")
	}
	if strings.ContainsAny(raw, "\r\x00") {
		return invalid("UNSUPPORTED_RUNTIME_VERSION")
	}
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	token := ""
	switch params["runtime"] {
	case "python", "node":
		if len(lines) != 1 {
			return invalid("UNSUPPORTED_RUNTIME_VERSION")
		}
		pattern := pythonVersionLine
		if params["runtime"] == "node" {
			pattern = nodeVersionLine
		}
		parts := pattern.FindStringSubmatch(lines[0])
		if len(parts) != 2 {
			return invalid("UNSUPPORTED_RUNTIME_VERSION")
		}
		token = parts[1]
	case "java":
		parts := javaVersionLine.FindStringSubmatch(lines[0])
		if len(parts) != 3 {
			return invalid("UNSUPPORTED_RUNTIME_VERSION")
		}
		token = parts[1]
		if token == "" {
			token = parts[2]
		}
		if !javaVersionToken.MatchString(token) {
			return invalid("UNSUPPORTED_RUNTIME_VERSION")
		}
		for _, line := range lines[1:] {
			if javaVersionLine.MatchString(line) {
				return invalid("UNSUPPORTED_RUNTIME_VERSION")
			}
		}
	default:
		return invalid("UNSUPPORTED_RUNTIME_VERSION")
	}
	numeric := runtimeNumericPrefix.FindStringSubmatch(token)
	if len(numeric) != 3 {
		return invalid("UNSUPPORTED_RUNTIME_VERSION")
	}
	components := strings.Split(strings.ReplaceAll(numeric[1], "_", "."), ".")
	for i, component := range components {
		value, err := strconv.ParseUint(component, 10, 64)
		if err != nil {
			return invalid("UNSUPPORTED_RUNTIME_VERSION")
		}
		components[i] = strconv.FormatUint(value, 10)
	}
	if params["runtime"] == "java" && len(components) > 1 && components[0] == "1" {
		components = components[1:]
	}
	version := strings.Join(components, ".")
	summary["version_token"] = token
	summary["normalized_version"] = version
	summary["version_suffix"] = numeric[2]
	return numericVersionBounds(version, params), summary
}
