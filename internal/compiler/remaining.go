package compiler

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/safeyaml"
)

func remainingCommand(id string, values map[string]plan.ResolvedParam) (string, bool) {
	switch id {
	case "H01":
		return `LC_ALL=C uname -m`, true
	case "H03":
		return `awk '/^MemTotal:/ {print; found=1} END {if (!found) exit 2}' /proc/meminfo`, true
	case "H04":
		return `cat /proc/swaps`, true
	case "R03":
		return `cat /proc/loadavg`, true
	case "L01":
		return `cat /proc/uptime`, true
	case "N03":
		return tcpCommand(values), true
	case "N05":
		return boundedHTTPCommand(values), true
	case "O03":
		path := "/proc/sys/" + strings.ReplaceAll(values["name"].Value.(string), ".", "/")
		return `out=$(LC_ALL=C stat -L -c %F -- ` + shellQuote(path) + ` 2>&1); rc=$?; if [ "$rc" -ne 0 ]; then case "$out" in *'No such file or directory'*|*'Not a directory'*) printf '__RS_SYSCTL_MISSING__'; exit 0;; *) exit 2;; esac; fi; printf '__RS_SYSCTL_VALUE__\n'; LC_ALL=C cat -- ` + shellQuote(path) + ` || exit 2`, true
	case "O04":
		return `cat /proc/modules`, true
	case "N02":
		return boundPortCommand(values), true
	case "S04", "S05":
		return filesystemCommand(values), true
	case "S01":
		if values["follow_symlinks"].Value != true {
			return "", false
		}
		return `out=$(LC_ALL=C stat -L -c %F -- ` + shellQuote(values["path"].Value.(string)) + ` 2>&1); rc=$?; if [ "$rc" -ne 0 ]; then case "$out" in *'No such file or directory'*|*'Not a directory'*) printf 'exists=false\n'; exit 0;; *) exit 2;; esac; fi; case "$out" in directory) kind=directory;; 'regular file'|'regular empty file') kind=file;; *) kind=other;; esac; printf 'exists=true\nfiletype=%s\n' "$kind"`, true
	case "O01":
		return `cat /etc/os-release`, true
	case "N01":
		name := shellQuote(values["name"].Value.(string))
		return `out=$(ip -j address show dev ` + name + ` 2>&1); rc=$?; if [ "$rc" -ne 0 ]; then case "$out" in *'does not exist'*) printf '__RS_INTERFACE_MISSING__\n'; exit 0;; *) exit 2;; esac; fi; printf '%s\n' "$out"`, true
	case "R05":
		return timeSyncCommand(values), true
	case "A01", "A02":
		return nssCommand(id, values), true
	case "A04":
		if values["framework"].Value == "apparmor" {
			command := `if [ -r /sys/module/apparmor/parameters/enabled ]; then state=$(cat /sys/module/apparmor/parameters/enabled) || exit 2; case "$state" in Y) :;; N) printf disabled; exit 0;; *) exit 2;; esac; elif [ -d /sys/module ] && [ ! -d /sys/module/apparmor ]; then printf disabled; exit 0; else exit 2; fi; `
			if values["mode"].Value == "enabled" || values["mode"].Value == "disabled" {
				return command + `printf enabled`, true
			}
			return command + `printf '__RS_APPARMOR_PROFILES__\n'; cat /sys/kernel/security/apparmor/profiles`, true
		}
		return `if command -v getenforce >/dev/null 2>&1; then getenforce; elif [ -r /sys/fs/selinux/enforce ]; then state=$(cat /sys/fs/selinux/enforce) || exit 2; case "$state" in 1) printf Enforcing;; 0) printf Permissive;; *) exit 2;; esac; elif [ -d /sys/fs ] && [ ! -d /sys/fs/selinux ]; then printf Disabled; else exit 2; fi`, true
	case "D01":
		return packageCommand(values), true
	case "D04":
		return elfCommand(values), true
	case "D05":
		return `cat -- ` + shellQuote(values["path"].Value.(string)), true
	default:
		return logCommand(id, values)
	}
}

func packageCommand(values map[string]plan.ResolvedParam) string {
	name := shellQuote(values["name"].Value.(string))
	manager := values["manager"].Value.(string)
	var command string
	if manager == "dpkg" {
		// Keep stderr out of the data stream: dpkg-query emits parser warnings
		// alongside otherwise valid output. Re-run only on failure so the
		// diagnostic can distinguish "not installed" from a broken database.
		command = `out=$(LC_ALL=C dpkg-query -W -f '${db:Status-Status}\t${Version}' -- ` + name + ` 2>/dev/null); rc=$?; if [ "$rc" -ne 0 ]; then diag=$(LC_ALL=C dpkg-query -W -f '${db:Status-Status}\t${Version}' -- ` + name + ` 2>&1 >/dev/null); case "$diag" in *'no packages found matching'*) printf 'installed=false\n'; exit 0;; *) exit 2;; esac; fi; count=$(printf '%s\n' "$out" | awk -F '\t' 'NF==2 && $1 ~ /^[a-z][a-z-]*$/ && $2 != "" {n++} END {print n+0}'); [ "$count" -eq 1 ] || exit 2; status=$(printf '%s\n' "$out" | cut -f1); version=$(printf '%s\n' "$out" | cut -f2); case "$version" in ''|*[!A-Za-z0-9.:+~_-]*) exit 2;; esac; if [ "$status" != installed ]; then printf 'installed=false\n'; exit 0; fi; printf 'installed=true\nversion=%s\n' "$version"; `
	} else {
		command = `out=$(LC_ALL=C rpm -q --qf '%{EPOCHNUM}:%{VERSION}-%{RELEASE}' -- ` + name + ` 2>/dev/null); rc=$?; if [ "$rc" -ne 0 ]; then diag=$(LC_ALL=C rpm -q --qf '%{EPOCHNUM}:%{VERSION}-%{RELEASE}' -- ` + name + ` 2>&1 >/dev/null); case "$diag" in *'is not installed'*) printf 'installed=false\n'; exit 0;; *) exit 2;; esac; fi; version=$out; case "$version" in ''|*[!A-Za-z0-9.:+~^_-]*) exit 2;; esac; printf 'installed=true\nversion=%s\n' "$version"; `
	}
	for _, bound := range []struct{ field, key, op string }{{"minimum_version", "minimum", "ge"}, {"maximum_version", "maximum", "le"}} {
		value, ok := values[bound.field]
		if !ok {
			continue
		}
		version := value.Value.(string)
		if manager == "dpkg" {
			command += `dpkg --compare-versions "$version" ` + bound.op + ` ` + shellQuote(version) + `; rc=$?; case "$rc" in 0) printf '` + bound.key + `=true\n';; 1) printf '` + bound.key + `=false\n';; *) exit 2;; esac; `
		} else {
			if !strings.Contains(version, ":") {
				version = "0:" + version
			}
			command += `n=$(rpm --eval '%{lua:print(rpm.vercmp("'"$version"'","` + version + `"))}') || exit 2; case "$n" in -1|0|1) :;; *) exit 2;; esac; if [ "$n" -` + bound.op + ` 0 ]; then printf '` + bound.key + `=true\n'; else printf '` + bound.key + `=false\n'; fi; `
		}
	}
	return command
}

func remainingEvaluation(id string, params map[string]any, text string) string {
	if id == "H01" {
		status, _ := architectureEvaluation(params, text)
		return status
	}
	match := func(ok bool) string {
		if ok {
			return "PASS"
		}
		return "FAIL"
	}
	switch id {
	case "R03":
		status, _ := loadEvaluation(params, text)
		return status
	case "L01":
		status, _ := uptimeEvaluation(params, text)
		return status
	case "H03", "H04":
		status, _ := memoryEvaluation(id, params, text)
		return status
	case "N03":
		return tcpEvaluation(params, text)
	case "N05":
		status, _ := boundedHTTPEvaluation(params, text)
		return status
	case "O03":
		status, _ := sysctlEvaluation(params, text)
		return status
	case "O04":
		status, _ := moduleEvaluation(params, text)
		return status
	case "N02":
		return evaluateBoundPort(params, text)
	case "S04", "S05":
		status, _ := filesystemEvaluation(id, params, text)
		return status
	case "S01":
		fields := recordFields(text)
		if fields["exists"] != "true" && fields["exists"] != "false" {
			return "ERROR"
		}
		exists := fields["exists"] == "true"
		if exists != params["exists"] {
			return "FAIL"
		}
		if expected, ok := params["filetype"]; ok && fields["filetype"] != expected {
			return "FAIL"
		}
		return "PASS"
	case "O01":
		status, _ := osReleaseEvaluation(params, text)
		return status
	case "N01":
		status, _ := interfaceEvaluation(params, text)
		return status
	case "R05":
		status, _ := timeSyncEvaluation(params, text)
		return status
	case "A01", "A02":
		return nssEvaluation(id, params, text)
	case "A04":
		if params["framework"] == "apparmor" && strings.HasPrefix(text, "__RS_APPARMOR_PROFILES__\n") {
			enforcing, complaining := 0, 0
			for _, line := range strings.Split(strings.TrimPrefix(text, "__RS_APPARMOR_PROFILES__\n"), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				switch {
				case strings.HasSuffix(line, " (enforce)"), strings.HasSuffix(line, " (kill)"):
					enforcing++
				case strings.HasSuffix(line, " (complain)"):
					complaining++
				default:
					return "ERROR"
				}
			}
			if enforcing+complaining == 0 {
				return "ERROR"
			}
			if params["mode"] == "enforcing" {
				return match(enforcing > 0 && complaining == 0)
			}
			if params["mode"] == "permissive" {
				return match(complaining > 0 && enforcing == 0)
			}
			return "ERROR"
		}
		actual := strings.ToLower(strings.TrimSpace(text))
		valid := map[string]bool{"enforcing": true, "permissive": true, "enabled": true, "disabled": true}
		if !valid[actual] {
			return "ERROR"
		}
		return match(actual == params["mode"])
	case "D01":
		fields := recordFields(text)
		if fields["installed"] != "true" && fields["installed"] != "false" {
			return "ERROR"
		}
		installed := fields["installed"] == "true"
		if installed != params["installed"] {
			return "FAIL"
		}
		if !installed {
			return "PASS"
		}
		if fields["version"] == "" {
			return "ERROR"
		}
		for _, bound := range []struct{ field, key string }{{"minimum_version", "minimum"}, {"maximum_version", "maximum"}} {
			if _, ok := params[bound.field]; ok {
				if fields[bound.key] == "false" {
					return "FAIL"
				}
				if fields[bound.key] != "true" {
					return "ERROR"
				}
			}
		}
		return "PASS"
	case "D04":
		status, _ := elfEvaluation(params, text)
		return status
	case "D05":
		key, _ := params["key"].(string)
		format, _ := params["format"].(string)
		var actual any
		if format == "json" || format == "yaml" {
			if format == "json" && !json.Valid([]byte(text)) {
				return "ERROR"
			}
			doc, err := safeyaml.Parse(strings.NewReader(text))
			if err != nil {
				return "ERROR"
			}
			actual = doc
			if !strings.HasPrefix(key, "/") {
				return "ERROR"
			}
			parts, ok := jsonPointerParts(key)
			if !ok {
				return "ERROR"
			}
			for _, part := range parts {
				switch value := actual.(type) {
				case map[string]any:
					var exists bool
					actual, exists = value[part]
					if !exists {
						return "FAIL"
					}
				case []any:
					index, ok := jsonPointerIndex(part)
					if !ok {
						return "ERROR"
					}
					if index < 0 || index >= len(value) {
						return "FAIL"
					}
					actual = value[index]
				default:
					return "FAIL"
				}
			}
		} else {
			section := ""
			found := false
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
					continue
				}
				if format == "ini" && strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
					section = strings.Trim(line, "[]")
					continue
				}
				name, value, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				name = strings.TrimSpace(strings.TrimPrefix(name, "export "))
				if section != "" {
					name = section + "." + name
				}
				if name != key {
					continue
				}
				if found {
					return "ERROR"
				}
				found = true
				value = strings.TrimSpace(value)
				if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
					value = value[1 : len(value)-1]
				}
				actual = value
			}
			if !found {
				return "FAIL"
			}
		}
		if pattern, ok := params["regex"].(string); ok {
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				return "ERROR"
			}
			value, ok := actual.(string)
			if !ok {
				data, err := json.Marshal(actual)
				if err != nil {
					return "ERROR"
				}
				value = string(data)
			}
			return match(compiled.MatchString(value))
		}
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(params["expected"])
		return match(string(a) == string(b))
	default:
		if id == "L02" || id == "L03" {
			status, _ := logEvaluation(id, params, text)
			return status
		}
		return "ERROR"
	}
}

// jsonPointerParts splits an RFC 6901 pointer and decodes "~1"/"~0"
// escapes. Any other "~" sequence is malformed rather than a literal key.
func jsonPointerParts(key string) ([]string, bool) {
	parts := strings.Split(strings.TrimPrefix(key, "/"), "/")
	for i, part := range parts {
		for j := 0; j < len(part); j++ {
			if part[j] != '~' {
				continue
			}
			if j+1 >= len(part) || (part[j+1] != '0' && part[j+1] != '1') {
				return nil, false
			}
			j++
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts, true
}

// jsonPointerIndex accepts only the RFC 6901 array index form: "0" or a
// nonzero digit followed by digits. Anything else (leading zero, "-",
// sign) is a malformed array token.
func jsonPointerIndex(part string) (int, bool) {
	if part == "" {
		return 0, false
	}
	if len(part) > 1 && part[0] == '0' {
		return 0, false
	}
	for _, digit := range part {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	index, err := strconv.Atoi(part)
	if err != nil {
		return 0, false
	}
	return index, true
}

func recordFields(text string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

func redactedEvidence(actual any) any {
	return map[string]any{"redacted": true, "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprint(actual))))}
}
