package compiler

import (
	"encoding/json"
	"math/big"
	"strings"
)

func memoryEvaluation(id string, params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{}
	parse := func(value string) (*big.Int, bool) {
		// procfs prints unsigned decimal counters, not signed values or the
		// alternate numeric forms accepted by big.Int.SetString.
		if value == "" {
			return nil, false
		}
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return nil, false
			}
		}
		n, ok := new(big.Int).SetString(value, 10)
		return n, ok && n.Sign() >= 0
	}
	bytes := func(kib *big.Int) *big.Int { return new(big.Int).Mul(kib, big.NewInt(1024)) }
	if id == "H03" {
		fields := strings.Fields(text)
		if len(fields) != 3 || fields[0] != "MemTotal:" || fields[2] != "kB" {
			return "ERROR", summary
		}
		kib, ok := parse(fields[1])
		if !ok || kib.Sign() == 0 {
			return "ERROR", summary
		}
		total := bytes(kib)
		summary["raw"] = strings.TrimSpace(text)
		summary["mem_total_kib"] = json.Number(kib.String())
		summary["mem_total_bytes"] = json.Number(total.String())
		return exactNumericMatch(map[string]any{"ge": params["min_memory"]}, total.String(), false), summary
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 0 || strings.Join(strings.Fields(lines[0]), " ") != "Filename Type Size Used Priority" {
		return "ERROR", summary
	}
	total := new(big.Int)
	devices := []map[string]any{}
	seenDevices := map[string]bool{}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return "ERROR", summary
		}
		if !strings.HasPrefix(fields[0], "/") || fields[0] == "/" || strings.ContainsRune(fields[0], 0) || seenDevices[fields[0]] || (fields[1] != "file" && fields[1] != "partition") {
			return "ERROR", summary
		}
		seenDevices[fields[0]] = true
		size, ok := parse(fields[2])
		if !ok {
			return "ERROR", summary
		}
		used, ok := parse(fields[3])
		if !ok || used.Cmp(size) > 0 {
			return "ERROR", summary
		}
		// Priorities may be negative; size and usage must remain unsigned.
		if _, ok := parse(strings.TrimPrefix(fields[4], "-")); !ok {
			return "ERROR", summary
		}
		priority, ok := new(big.Int).SetString(fields[4], 10)
		if !ok {
			return "ERROR", summary
		}
		total.Add(total, bytes(size))
		devices = append(devices, map[string]any{"path": fields[0], "type": fields[1], "size_bytes": json.Number(bytes(size).String()), "used_bytes": json.Number(bytes(used).String()), "priority": json.Number(priority.String())})
	}
	summary["devices"] = devices
	summary["total_bytes"] = json.Number(total.String())
	summary["active"] = len(devices) > 0
	passed := false
	switch params["mode"] {
	case "disabled":
		passed = len(devices) == 0
	case "enabled":
		passed = len(devices) > 0
	case "minimum":
		return exactNumericMatch(map[string]any{"ge": params["min_swap"]}, total.String(), false), summary
	default:
		return "ERROR", summary
	}
	if passed {
		return "PASS", summary
	}
	return "FAIL", summary
}
