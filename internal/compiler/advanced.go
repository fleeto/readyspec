package compiler

import (
	"fmt"

	"github.com/fleeto/readyspec/internal/plan"
)

func advancedCommand(id string, values map[string]plan.ResolvedParam) (string, bool) {
	switch id {
	case "R01":
		return `systemctl show --no-pager --all -p LoadState -p ActiveState -p UnitFileState -- ` + shellQuote(values["name"].Value.(string)), true
	case "O02":
		return `uname -r`, true
	case "R02":
		return `LC_ALL=C ps -eo pid=,comm= 2>&1`, true
	case "R04":
		if unit, ok := values["unit"]; ok {
			property := "Limit" + values["limit"].Value.(string)
			if values["bound"].Value == "soft" {
				property += "Soft"
			}
			return `systemctl show --no-pager --value -p ` + property + ` -- ` + shellQuote(unit.Value.(string)), true
		}
		return `cat /proc/` + fmt.Sprint(values["pid"].Value) + `/limits`, true
	case "D02":
		return commandMetadataCommand(values["command"].Value.(string)), true
	case "D03":
		return runtimeCommand(values), true
	case "N06":
		command := `ip -j route get ` + shellQuote(values["target"].Value.(string))
		if source, ok := values["source"]; ok {
			command += ` from ` + shellQuote(source.Value.(string))
		}
		return command, true
	default:
		return remainingCommand(id, values)
	}
}

func evaluateObservation(id string, expected any, actual any) string {
	if id == "S01_METADATA" {
		text, ok := actual.(string)
		if !ok {
			return "ERROR"
		}
		status, _ := pathMetadataEvaluation(text)
		return status
	}
	params, ok := expected.(map[string]any)
	if !ok {
		return "ERROR"
	}
	text, ok := actual.(string)
	if !ok {
		return "ERROR"
	}
	switch id {
	case "R01":
		status, _ := serviceEvaluation(params, text)
		return status
	case "O02":
		status, _ := kernelEvaluation(params, text)
		return status
	case "D03":
		status, _ := runtimeEvaluation(params, text)
		return status
	case "R02":
		status, _ := processEvaluation(params, text)
		return status
	case "R04":
		status, _ := limitsEvaluation(params, text)
		return status
	case "D02":
		status, _ := commandMetadataEvaluation(params, text)
		return status
	case "N04_RCODE":
		text, ok := actual.(string)
		if !ok {
			return "ERROR"
		}
		status, _ := dnsRcodeEvaluation(text)
		return status
	case "N06":
		status, _ := routeEvaluation(params, text)
		return status
	default:
		return remainingEvaluation(id, params, text)
	}
}

func compareVersions(actual, expected string) (int, bool) {
	return plan.CompareNumericVersions(actual, expected)
}
