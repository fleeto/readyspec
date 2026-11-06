package compiler

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

func logCommand(id string, values map[string]plan.ResolvedParam) (string, bool) {
	if id != "L02" && id != "L03" {
		return "", false
	}
	filter := " _TRANSPORT=kernel"
	if id == "L03" {
		filter = " -u " + shellQuote(values["unit"].Value.(string))
	}
	query := `journalctl --system --no-pager --quiet -o json` + filter + ` 2>&1`
	// journalctl can silently omit inaccessible system journals. Prove access to
	// every system journal before accepting coverage from a filtered query.
	access := `for dir in /var/log/journal /run/log/journal; do if [ -d "$dir" ]; then [ -r "$dir" ] && [ -x "$dir" ] || exit 2; inaccessible=$(find "$dir" \( \( -type d ! -executable \) -o \( -type f -name '*.journal*' ! -readable \) \) -print) || exit 2; [ -z "$inaccessible" ] || exit 2; fi; done; `
	command := access + `now=$(date +%s) || exit 2; up=$(awk '{printf "%.0f", int($1)}' /proc/uptime) || exit 2; case "$now:$up" in *[!0-9:]*) exit 2;; esac; start=$((now-` + fmt.Sprint(values["lookback_seconds"].Value) + `)); boot=$((now-up)); printf '__RS_WINDOW__\n%s\n%s\n%s\n__RS_OLDER__\n' "$now" "$start" "$boot"; ` + query + ` --until "@$start" -n 1 || exit 2; printf '__RS_BOOT_START__\n'; ` + query + ` --boot=0 --since "@$boot" --until "@$((boot+60))" -n 1 || exit 2; printf '__RS_EVENTS__\n'; ` + query + ` --since "@$start" --until "@$now" -n 10000 || exit 2`
	// Filtered cursors legitimately skip unrelated messages. Audit the unfiltered
	// system stream to detect missing archives and rate-limited/dropped records.
	audit := `journalctl --system --no-pager --quiet -o json 2>&1`
	command += `; printf '__RS_AUDIT_OLDER__\n'; ` + audit + ` --until "@$start" -n 1 || exit 2; printf '__RS_AUDIT__\n'; ` + audit + ` --since "@$start" --until "@$now" -n 10000 || exit 2; printf '__RS_AUDIT_END__\n'`
	return command, true
}

type journalEntry struct {
	Timestamp        string `json:"__REALTIME_TIMESTAMP"`
	Message          string `json:"MESSAGE"`
	Cursor           string `json:"__CURSOR"`
	Invocation       string `json:"INVOCATION_ID"`
	SystemInvocation string `json:"_SYSTEMD_INVOCATION_ID"`
	BootID           string `json:"_BOOT_ID"`
}

func logEvaluation(id string, params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{"source": "system journal", "redacted": true}
	if !strings.HasPrefix(text, "__RS_WINDOW__\n") {
		return "ERROR", summary
	}
	sections := strings.Split(text, "__RS_OLDER__\n")
	if len(sections) != 2 {
		return "ERROR", summary
	}
	window := strings.Split(strings.TrimSpace(strings.TrimPrefix(sections[0], "__RS_WINDOW__\n")), "\n")
	if len(window) != 3 {
		return "ERROR", summary
	}
	now, e1 := strconv.ParseInt(window[0], 10, 64)
	start, e2 := strconv.ParseInt(window[1], 10, 64)
	boot, e3 := strconv.ParseInt(window[2], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || now <= 0 || boot <= 0 || start < 0 || start > now || boot > now {
		return "ERROR", summary
	}
	summary["window_start"] = start
	summary["window_end"] = now
	parts := strings.Split(sections[1], "__RS_BOOT_START__\n")
	if len(parts) != 2 {
		return "ERROR", summary
	}
	tail := strings.Split(parts[1], "__RS_EVENTS__\n")
	if len(tail) != 2 {
		return "ERROR", summary
	}
	parse := func(raw string) ([]journalEntry, bool) {
		entries := []journalEntry{}
		for _, line := range strings.Split(raw, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var entry journalEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil || entry.Timestamp == "" {
				return nil, false
			}
			entries = append(entries, entry)
		}
		return entries, true
	}
	older, ok := parse(parts[0])
	if !ok {
		return "ERROR", summary
	}
	first, ok := parse(tail[0])
	if !ok {
		return "ERROR", summary
	}
	eventSections := strings.Split(tail[1], "__RS_AUDIT_OLDER__\n")
	if len(eventSections) != 2 {
		return "ERROR", summary
	}
	auditSections := strings.Split(eventSections[1], "__RS_AUDIT__\n")
	if len(auditSections) != 2 || !strings.HasSuffix(auditSections[1], "__RS_AUDIT_END__\n") {
		return "ERROR", summary
	}
	events, ok := parse(eventSections[0])
	if !ok {
		return "ERROR", summary
	}
	auditOlder, olderValid := parse(auditSections[0])
	audit, auditValid := parse(strings.TrimSuffix(auditSections[1], "__RS_AUDIT_END__\n"))
	timestamp := func(e journalEntry) (int64, bool) {
		n, err := strconv.ParseInt(e.Timestamp, 10, 64)
		return n / 1000000, err == nil && n > 0
	}
	covered := false
	for _, entry := range older {
		when, valid := timestamp(entry)
		if valid && when <= start {
			covered = true
			summary["coverage_timestamp"] = when
		}
	}
	if !covered && params["allow_since_boot"] == true && boot >= start {
		for _, entry := range first {
			when, valid := timestamp(entry)
			if valid && when >= boot && when <= boot+60 && when <= now {
				covered = true
				summary["coverage_timestamp"] = when
				summary["since_boot"] = true
			}
		}
	}
	count, failures, restarts := 0, 0, 0
	type oomRecord struct {
		time int64
		pid  string
	}
	markers, kills := []oomRecord{}, []oomRecord{}
	ambiguous := false
	seen := map[string]bool{}
	for _, entry := range events {
		when, valid := timestamp(entry)
		if !valid || when < start || when > now {
			return "ERROR", summary
		}
		key := entry.Cursor
		if key == "" {
			key = entry.Timestamp + entry.Message
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		message := strings.ToLower(entry.Message)
		if id == "L02" {
			if match := regexp.MustCompile(`(?i)killed process\s+([0-9]+)`).FindStringSubmatch(entry.Message); len(match) > 1 {
				count++
				kills = append(kills, oomRecord{when, match[1]})
			} else if strings.Contains(message, "kernel panic") || strings.Contains(message, "oops:") {
				count++
			} else if strings.Contains(message, "oom-kill:") || strings.Contains(message, "out of memory") {
				pid := ""
				if match := regexp.MustCompile(`(?:^|[, ])pid=([0-9]+)`).FindStringSubmatch(message); len(match) > 1 {
					pid = match[1]
				}
				markers = append(markers, oomRecord{when, pid})
			}
		} else {
			if strings.Contains(message, "scheduled restart job") {
				restarts++
			}
			if strings.Contains(message, "failed with result") || (strings.Contains(message, "main process exited") && !strings.Contains(message, "status=0/success")) {
				invocation := entry.Invocation
				if invocation == "" {
					invocation = entry.SystemInvocation
				}
				if invocation == "" {
					ambiguous = true
					invocation = entry.Timestamp
				}
				if !seen["failure:"+invocation] {
					seen["failure:"+invocation] = true
					failures++
				}
			}
		}
	}
	for _, marker := range markers {
		associated := false
		for _, kill := range kills {
			if marker.pid != "" && marker.pid == kill.pid && marker.time >= kill.time-1 && marker.time <= kill.time+1 {
				associated = true
				break
			}
		}
		if !associated {
			ambiguous = true
		}
	}
	summary["events"] = count
	summary["failures"] = failures
	summary["restarts"] = restarts
	summary["window_boundary_proven"] = covered
	continuous, reason := journalContinuity(auditOlder, audit, start, now, boot, params["allow_since_boot"] == true, first)
	if !olderValid || !auditValid {
		continuous = false
		reason = "INVALID_AUDIT"
	}
	summary["continuity_proven"] = continuous
	summary["continuity_reason"] = reason
	summary["continuity_scope"] = "unfiltered system journal cursor sequence"
	summary["record_limit"] = 10000
	summary["audit_records"] = len(audit)
	summary["truncated"] = len(events) >= 10000 || len(audit) >= 10000
	summary["coverage_proven"] = covered && continuous && !ambiguous && len(events) < 10000 && len(audit) < 10000
	limit := func(key string) float64 {
		switch v := params[key].(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case int64:
			return float64(v)
		case json.Number:
			n, _ := v.Float64()
			return n
		}
		return 0
	}
	if (id == "L02" && float64(count) > limit("max_events")) || (id == "L03" && (float64(failures) > limit("max_failures") || float64(restarts) > limit("max_restarts"))) {
		return "FAIL", summary
	}
	if id == "L02" && len(markers) > 0 && limit("max_events") == 0 {
		return "FAIL", summary
	}
	if !covered || !continuous || ambiguous || len(events) >= 10000 || len(audit) >= 10000 {
		return "ERROR", summary
	}
	return "PASS", summary
}
