package compiler

import (
	"strconv"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

func timeSyncCommand(values map[string]plan.ResolvedParam) string {
	command := `printf '__RS_TIME_SYNC__\n'; timedatectl show -p NTPSynchronized --value 2>&1; rc=$?; printf '\n__RS_TIME_SYNC_RC__\n%s\n' "$rc"; `
	services, _ := values["allowed_services"].Value.([]any)
	for _, service := range services {
		command += `printf '__RS_SERVICE__\n%s\n' ` + shellQuote(service.(string)) + `; systemctl is-active ` + shellQuote(service.(string)) + ` 2>&1; rc=$?; printf '\n__RS_SERVICE_RC__\n%s\n' "$rc"; `
	}
	return command + `printf '__RS_TIME_END__\n'`
}

func timeSyncEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{}
	invalid := func(reason string) (string, map[string]any) { summary["error"] = reason; return "ERROR", summary }
	scalar := func(value string) (string, bool) {
		value = strings.TrimSuffix(value, "\n")
		return value, value != "" && !strings.ContainsAny(value, "\r\n\x00")
	}
	parse := func(value, marker string) (string, int, bool) {
		parts := strings.Split(value, "\n"+marker+"\n")
		if len(parts) != 2 {
			return "", 0, false
		}
		observed := strings.TrimSuffix(parts[0], "\n")
		if strings.ContainsAny(observed, "\r\n\x00") {
			return "", 0, false
		}
		exit, ok := scalar(parts[1])
		if !ok {
			return "", 0, false
		}
		rc, err := strconv.Atoi(exit)
		return observed, rc, err == nil && rc >= 0 && rc <= 255
	}
	if !strings.HasPrefix(text, "__RS_TIME_SYNC__\n") || !strings.HasSuffix(text, "__RS_TIME_END__\n") {
		return invalid("INVALID_TIME_OBSERVATION")
	}
	text = strings.TrimSuffix(strings.TrimPrefix(text, "__RS_TIME_SYNC__\n"), "__RS_TIME_END__\n")
	sections := strings.Split(text, "__RS_SERVICE__\n")
	services, ok := params["allowed_services"].([]any)
	if !ok || len(services) == 0 || len(sections) != len(services)+1 {
		return invalid("INVALID_TIME_OBSERVATION")
	}
	sync, rc, ok := parse(sections[0], "__RS_TIME_SYNC_RC__")
	if !ok {
		return invalid("INVALID_SYNC_OBSERVATION")
	}
	summary["sync_exit_status"] = rc
	if rc != 0 {
		return invalid("SYNC_QUERY_FAILED")
	}
	if sync != "yes" && sync != "no" {
		return invalid("INVALID_SYNC_OBSERVATION")
	}
	summary["synchronized"] = sync == "yes"
	queries := []map[string]any{}
	active := []string{}
	for index, service := range services {
		name, ok := service.(string)
		if !ok {
			return invalid("INVALID_SERVICE_OBSERVATION")
		}
		observed, found := strings.CutPrefix(sections[index+1], name+"\n")
		if !found {
			return invalid("INVALID_SERVICE_OBSERVATION")
		}
		state, rc, ok := parse(observed, "__RS_SERVICE_RC__")
		if !ok {
			return invalid("INVALID_SERVICE_OBSERVATION")
		}
		if rc != 0 && rc != 3 && rc != 4 {
			queries = append(queries, map[string]any{"name": name, "exit_status": rc})
			summary["service_queries"] = queries
			return invalid("SERVICE_QUERY_FAILED")
		}
		known := map[string]bool{"active": true, "reloading": true, "refreshing": true, "inactive": true, "failed": true, "activating": true, "deactivating": true, "maintenance": true, "unknown": true}
		if !known[state] {
			return invalid("INVALID_SERVICE_STATE")
		}
		queries = append(queries, map[string]any{"name": name, "state": state, "exit_status": rc})
		summary["service_queries"] = queries
		valid := false
		switch rc {
		case 0:
			valid = state == "active" || state == "reloading" || state == "refreshing"
			if valid {
				active = append(active, name)
			}
		case 3, 4:
			// Exit 4 indicates a missing unit file; systemd may still print
			// its active-state value (typically inactive), not "unknown".
			valid = state == "inactive" || state == "failed" || state == "activating" || state == "deactivating" || state == "maintenance" || state == "unknown"
		default:
			return invalid("SERVICE_QUERY_FAILED")
		}
		if !valid {
			return invalid("INCONSISTENT_SERVICE_STATE")
		}
	}
	summary["active_services"] = active
	expected, ok := params["synchronized"].(bool)
	if !ok {
		return invalid("INVALID_TIME_ASSERTION")
	}
	if (sync == "yes") != expected || len(active) == 0 {
		return "FAIL", summary
	}
	return "PASS", summary
}
