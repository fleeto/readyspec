package compiler

import "strings"

func serviceEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{"name": params["name"]}
	invalid := func() (string, map[string]any) {
		summary["error"] = "INVALID_UNIT_OBSERVATION"
		return "ERROR", summary
	}
	if strings.ContainsAny(text, "\r\x00") {
		return invalid()
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || (key != "LoadState" && key != "ActiveState" && key != "UnitFileState") {
			return invalid()
		}
		if _, duplicate := fields[key]; duplicate {
			return invalid()
		}
		fields[key] = value
	}
	if len(fields) != 3 {
		return invalid()
	}
	active, unit, load := fields["ActiveState"], fields["UnitFileState"], fields["LoadState"]
	activeStates := map[string]bool{"active": true, "reloading": true, "refreshing": true, "inactive": true, "failed": true, "activating": true, "deactivating": true, "maintenance": true}
	if !activeStates[active] {
		return invalid()
	}
	if load == "not-found" {
		if active != "inactive" || unit != "" {
			return invalid()
		}
		summary["exists"] = false
		summary["load_state"] = load
		summary["active_state"] = active
		summary["unit_file_state"] = unit
		return "FAIL", summary
	}
	if load != "loaded" && load != "masked" {
		return invalid()
	}
	unitStates := map[string]bool{"enabled": true, "enabled-runtime": true, "disabled": true, "static": true, "indirect": true, "linked": true, "linked-runtime": true, "alias": true, "masked": true, "masked-runtime": true, "generated": true, "transient": true}
	if !unitStates[unit] {
		return invalid()
	}
	running := active == "active" || active == "reloading" || active == "refreshing"
	enabled := unit == "enabled" || unit == "enabled-runtime"
	summary["exists"] = true
	summary["load_state"] = load
	summary["active_state"] = active
	summary["unit_file_state"] = unit
	summary["running"] = running
	summary["enabled"] = enabled
	expectedRunning, rOK := params["running"].(bool)
	expectedEnabled, eOK := params["enabled"].(bool)
	if !rOK || !eOK {
		return invalid()
	}
	if running != expectedRunning || enabled != expectedEnabled {
		return "FAIL", summary
	}
	return "PASS", summary
}
