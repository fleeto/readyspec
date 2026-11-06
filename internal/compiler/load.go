package compiler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var loadDecimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
var loadCounter = regexp.MustCompile(`^[0-9]+$`)

// Read a complete procfs record before trusting any selected window. Keep
// comparison in decimal arithmetic; load is a queue average, not CPU usage.
func loadEvaluation(params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{}
	invalid := func() (string, map[string]any) {
		summary["error"] = "INVALID_LOADAVG"
		return "ERROR", summary
	}
	line := strings.TrimSuffix(text, "\n")
	if strings.ContainsAny(line, "\n\r\x00") {
		return invalid()
	}
	for _, character := range line {
		if character != ' ' && character != '\t' && (character < 33 || character > 126) {
			return invalid()
		}
	}
	fields := strings.Fields(line)
	if len(fields) != 5 {
		return invalid()
	}
	for _, value := range fields[:3] {
		if !loadDecimal.MatchString(value) {
			return invalid()
		}
	}
	counter := func(value string) (uint64, bool) {
		if !loadCounter.MatchString(value) {
			return 0, false
		}
		n, err := strconv.ParseUint(value, 10, 64)
		return n, err == nil
	}
	queue := strings.Split(fields[3], "/")
	if len(queue) != 2 {
		return invalid()
	}
	if _, ok := counter(queue[0]); !ok {
		return invalid()
	}
	if total, ok := counter(queue[1]); !ok || total == 0 {
		return invalid()
	}
	if _, ok := counter(fields[4]); !ok {
		return invalid()
	}
	window, err := strconv.Atoi(fmt.Sprint(params["window"]))
	index := map[int]int{1: 0, 5: 1, 15: 2}
	selected, ok := index[window]
	if err != nil || !ok {
		return invalid()
	}
	summary["window_minutes"] = window
	summary["load_average"] = fields[selected]
	summary["maximum"] = params["maximum"]
	summary["measurement"] = "average runnable or uninterruptible tasks"
	return exactNumericMatch(map[string]any{"le": params["maximum"]}, fields[selected], false), summary
}
