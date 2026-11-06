package compiler

import (
	"regexp"
	"strconv"
	"strings"
)

var limitInteger = regexp.MustCompile(`^[0-9]+$`)

func limitsEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{"limit": params["limit"], "bound": params["bound"]}
	invalid := func() (string, map[string]any) {
		summary["error"] = "INVALID_LIMIT_OBSERVATION"
		return "ERROR", summary
	}
	if strings.ContainsAny(text, "\x00\r") {
		return invalid()
	}
	parse := func(value string) (uint64, bool, bool) {
		if value == "unlimited" || value == "infinity" {
			return 0, true, true
		}
		if !limitInteger.MatchString(value) {
			return 0, false, false
		}
		number, err := strconv.ParseUint(value, 10, 64)
		return number, false, err == nil
	}
	selected := ""
	if unit, ok := params["unit"]; ok {
		summary["unit"] = unit
		summary["source_kind"] = "configured_unit_limit"
		selected = strings.Trim(strings.TrimSuffix(text, "\n"), " \t")
		if strings.ContainsRune(selected, '\n') {
			return invalid()
		}
	} else {
		summary["pid"] = params["pid"]
		summary["source_kind"] = "effective_process_limit"
		label, units := "Max open files", "files"
		if params["limit"] == "NPROC" {
			label, units = "Max processes", "processes"
		} else if params["limit"] != "NOFILE" {
			return invalid()
		}
		found := false
		softRaw, hardRaw := "", ""
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimLeft(line, " \t")
			if !strings.HasPrefix(line, label) {
				continue
			}
			rest := strings.TrimPrefix(line, label)
			if found || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
				return invalid()
			}
			found = true
			fields := strings.Fields(rest)
			if len(fields) != 3 || fields[2] != units {
				return invalid()
			}
			soft, softUnlimited, softOK := parse(fields[0])
			hard, hardUnlimited, hardOK := parse(fields[1])
			// procfs uses 'unlimited', while systemctl uses 'infinity'.
			if !softOK || !hardOK || fields[0] == "infinity" || fields[1] == "infinity" || (!hardUnlimited && (softUnlimited || soft > hard)) {
				return invalid()
			}
			softRaw, hardRaw = fields[0], fields[1]
			selected = fields[0]
			if params["bound"] == "hard" {
				selected = fields[1]
			}
		}
		if !found {
			return invalid()
		}
		summary["soft"] = softRaw
		summary["hard"] = hardRaw
		summary["units"] = units
	}
	_, unlimited, ok := parse(selected)
	if !ok {
		return invalid()
	}
	if params["bound"] != "soft" && params["bound"] != "hard" {
		return invalid()
	}
	summary["value"] = selected
	summary["unlimited"] = unlimited
	summary["minimum"] = params["minimum"]
	if unlimited {
		return "PASS", summary
	}
	return exactNumericMatch(map[string]any{"ge": params["minimum"]}, selected, false), summary
}
