//go:build linux

package compiler_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const journalStream = "0123456789abcdef0123456789abcdef"

func journalRecord(second int, sequence int, message string) string {
	raw, _ := json.Marshal(map[string]any{"__REALTIME_TIMESTAMP": fmt.Sprint(int64(second) * 1000000), "__CURSOR": fmt.Sprintf("s=%s;i=%x;b=%s;m=1;t=1;x=1", journalStream, sequence, journalStream), "_BOOT_ID": journalStream, "MESSAGE": message})
	return string(raw) + "\n"
}

func journalFixtureTools(t *testing.T, root, id string, boot int) {
	t.Helper()
	fixtureFile(t, root, "proc/uptime", fmt.Sprintf("%d.0 0\n", 10000-boot))
	fixtureFile(t, root, "var/log/journal/machine/system.journal", "")
	fixtureTool(t, root, "tools/date", `[ "$*" = '+%s' ]; printf 10000`)
	filter := "_TRANSPORT=kernel"
	if id == "L03" {
		filter = "-u app.service"
	}
	fixtureTool(t, root, "tools/journalctl", `if [ -f /warning ]; then cat /warning >&2; fi
case "$*" in
 '--system --no-pager --quiet -o json `+filter+` --until @6400 -n 1') cat /older;;
 '--system --no-pager --quiet -o json `+filter+` --boot=0 --since @`+fmt.Sprint(boot)+` --until @`+fmt.Sprint(boot+60)+` -n 1') cat /first;;
 '--system --no-pager --quiet -o json `+filter+` --since @6400 --until @10000 -n 10000') cat /events;;
 '--system --no-pager --quiet -o json --until @6400 -n 1') cat /audit-older;;
 '--system --no-pager --quiet -o json --since @6400 --until @10000 -n 10000') cat /audit;;
 *) exit 2;;
esac`)
}

func TestJournalCollectorsRejectIncompleteHistory(t *testing.T) {
	for _, id := range []string{"L02", "L03"} {
		for _, name := range []string{"covered_zero", "since_boot_allowed", "since_boot_disallowed", "since_boot_missing_head", "since_boot_old_boot", "since_boot_gap", "since_boot_late_head", "rotated_contiguous", "rotation_gap_zero", "rotation_gap_violation", "suppressed_zero", "truncated_zero", "audit_truncated_zero", "truncated_violation", "read_failure", "boundary_read_failure", "audit_read_failure", "malformed_entry", "invalid_cursor", "changed_sequence_domain", "malformed_audit", "audit_outside_window", "event_outside_window", "read_warning_zero"} {
			t.Run(id+"/"+name, func(t *testing.T) {
				root := collectionRoot(t)
				boot := 1000
				older := journalRecord(5000, 10, "older")
				first := ""
				events := ""
				auditOlder := older
				audit := journalRecord(7000, 11, "ordinary activity")
				allow := false
				want := "PASS"
				switch name {
				case "since_boot_allowed", "since_boot_disallowed", "since_boot_missing_head", "since_boot_old_boot", "since_boot_gap", "since_boot_late_head":
					boot = 7000
					older = ""
					auditOlder = ""
					first = journalRecord(7001, 100, "startup")
					audit = first + journalRecord(8000, 101, "ordinary activity")
					want = "ERROR"
					allow = name != "since_boot_disallowed"
					if name == "since_boot_allowed" {
						want = "PASS"
					}
					if name == "since_boot_missing_head" {
						first = ""
					}
					if name == "since_boot_gap" {
						audit = first + journalRecord(8000, 105, "after missing archive")
					}
					if name == "since_boot_late_head" {
						first = journalRecord(7080, 100, "late startup")
						audit = first + journalRecord(8000, 101, "ordinary activity")
					}
					if name == "since_boot_old_boot" {
						boot = 1000
						first = journalRecord(1001, 100, "startup")
					}
				case "rotated_contiguous":
					audit = journalRecord(6500, 11, "archived file tail") + journalRecord(7000, 12, "active file head")
				case "rotation_gap_zero":
					audit = journalRecord(6500, 11, "archived file tail") + journalRecord(7000, 20, "active file after missing archive")
					want = "ERROR"
				case "rotation_gap_violation":
					audit = journalRecord(6500, 11, "archive tail") + journalRecord(7000, 20, "after missing archive")
					message := "Killed process 42 (app)"
					if id == "L03" {
						message = "Scheduled restart job"
					}
					events = journalRecord(7000, 20, message)
					want = "FAIL"
				case "suppressed_zero":
					audit = journalRecord(7000, 11, "Suppressed 10 messages from app.service")
					want = "ERROR"
				case "truncated_zero", "truncated_violation", "audit_truncated_zero":
					var output strings.Builder
					for i := 0; i < 10000; i++ {
						output.WriteString(journalRecord(7000, 11+i, "ordinary activity"))
					}
					events = output.String()
					audit = events
					want = "ERROR"
					if name == "audit_truncated_zero" {
						events = ""
					}
					if name == "truncated_violation" {
						message := "Killed process 42 (app)"
						if id == "L03" {
							message = "Scheduled restart job, restart counter is at 1."
						}
						events = journalRecord(7000, 11, message) + strings.TrimPrefix(events, journalRecord(7000, 11, "ordinary activity"))
						want = "FAIL"
					}
				case "invalid_cursor":
					audit = strings.ReplaceAll(audit, ";i=b;", ";i=invalid;")
					want = "ERROR"
				case "changed_sequence_domain":
					audit = strings.ReplaceAll(audit, "s="+journalStream, "s=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
					want = "ERROR"
				case "malformed_audit":
					audit = "{broken JSON\n"
					want = "ERROR"
				case "audit_outside_window":
					audit = journalRecord(10001, 11, "future")
					want = "ERROR"
				case "event_outside_window":
					events = journalRecord(10001, 11, "future")
					want = "ERROR"
				case "read_failure", "boundary_read_failure", "audit_read_failure", "malformed_entry", "read_warning_zero":
					want = "ERROR"
				}
				journalFixtureTools(t, root, id, boot)
				for path, text := range map[string]string{"older": older, "first": first, "events": events, "audit-older": auditOlder, "audit": audit} {
					fixtureFile(t, root, path, text)
				}
				failedFiles := map[string]string{"read_failure": "events", "boundary_read_failure": "older", "audit_read_failure": "audit"}
				if path, ok := failedFiles[name]; ok {
					if err := os.Chmod(filepath.Join(root, path), 0000); err != nil {
						t.Fatal(err)
					}
				}
				if name == "malformed_entry" {
					fixtureFile(t, root, "events", "{broken JSON\n")
				}
				if name == "read_warning_zero" {
					fixtureFile(t, root, "warning", "Journal file was truncated, ignoring file.\n")
				}
				params := map[string]any{"lookback_seconds": float64(3600), "allow_since_boot": allow}
				if id == "L03" {
					params["unit"] = "app.service"
				}
				results := collectInSandbox(t, root, collectionArtifact(t, id, params))
				if len(results) != 1 || results[0].Status != want {
					t.Fatalf("want %s got %+v", want, results)
				}
				if name == "since_boot_allowed" && evidenceProperty(t, results, "since_boot") != true {
					t.Fatal("since boot coverage evidence missing")
				}
				if name == "rotation_gap_zero" && evidenceProperty(t, results, "continuity_proven") != false {
					t.Fatal("sequence gap was not recorded")
				}
				if name == "truncated_zero" && evidenceProperty(t, results, "truncated") != true {
					t.Fatal("truncation evidence missing")
				}
				reasons := map[string]string{"rotation_gap_zero": "SEQUENCE_GAP", "suppressed_zero": "MESSAGES_LOST", "invalid_cursor": "INVALID_CURSOR", "changed_sequence_domain": "SEQUENCE_DOMAIN_CHANGED", "malformed_audit": "INVALID_AUDIT", "audit_outside_window": "INVALID_AUDIT_TIME", "audit_truncated_zero": "AUDIT_TRUNCATED"}
				if reason, ok := reasons[name]; ok {
					if actual := evidenceProperty(t, results, "continuity_reason"); actual != reason {
						t.Fatalf("want reason %s got %v", reason, actual)
					}
					if evidenceProperty(t, results, "coverage_proven") != false {
						t.Fatal("incomplete history claimed coverage")
					}
				}
				if want == "PASS" && evidenceProperty(t, results, "coverage_proven") != true {
					t.Fatal("valid history lost its coverage proof")
				}
				raw, _ := json.Marshal(results)
				if strings.Contains(string(raw), "ordinary activity") || strings.Contains(string(raw), "Suppressed 10") {
					t.Fatal("raw journal messages leaked into normalized evidence")
				}
			})
		}
	}
}

func TestJournalCollectorsCountAndDeduplicateActualObservations(t *testing.T) {
	serviceEntry := func(second, sequence int, message, invocation string) string {
		var fields map[string]any
		_ = json.Unmarshal([]byte(journalRecord(second, sequence, message)), &fields)
		fields["_SYSTEMD_INVOCATION_ID"] = invocation
		raw, _ := json.Marshal(fields)
		return string(raw) + "\n"
	}
	for _, tt := range []struct {
		name, id, events string
		params           map[string]any
		status, field    string
		count            int
	}{
		{"oom_single_episode", "L02", journalRecord(7000, 11, "oom-kill: task=app,pid=42") + journalRecord(7001, 12, "Killed process 42 (app)"), map[string]any{"max_events": float64(1)}, "PASS", "events", 1},
		{"oom_unassociated_pid", "L02", journalRecord(7000, 11, "oom-kill: task=app,pid=43") + journalRecord(7001, 12, "Killed process 42 (app)"), map[string]any{"max_events": float64(1)}, "ERROR", "events", 1},
		{"duplicate_oom_cursor", "L02", journalRecord(7000, 11, "Killed process 42 (app)") + journalRecord(7000, 11, "Killed process 42 (app)"), map[string]any{"max_events": float64(1)}, "PASS", "events", 1},
		{"service_failure_episode", "L03", serviceEntry(7000, 11, "Main process exited, status=1/FAILURE", "one") + serviceEntry(7001, 12, "Failed with result exit-code", "one"), map[string]any{"max_failures": float64(1)}, "PASS", "failures", 1},
		{"service_failure_over_limit", "L03", serviceEntry(7000, 11, "Main process exited, status=1/FAILURE", "one") + serviceEntry(7001, 12, "Failed with result exit-code", "two"), map[string]any{"max_failures": float64(1)}, "FAIL", "failures", 2},
		{"service_restarts", "L03", journalRecord(7000, 11, "Scheduled restart job, restart counter is at 1."), map[string]any{"max_restarts": float64(1)}, "PASS", "restarts", 1},
		{"service_clean_exit", "L03", journalRecord(7000, 11, "Main process exited, status=0/SUCCESS"), nil, "PASS", "failures", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			journalFixtureTools(t, root, tt.id, 1000)
			for path, text := range map[string]string{"older": journalRecord(5000, 10, "older"), "first": "", "events": tt.events, "audit-older": journalRecord(5000, 10, "older"), "audit": tt.events} {
				fixtureFile(t, root, path, text)
			}
			params := map[string]any{"lookback_seconds": float64(3600)}
			if tt.id == "L03" {
				params["unit"] = "app.service"
			}
			for key, value := range tt.params {
				params[key] = value
			}
			results := collectInSandbox(t, root, collectionArtifact(t, tt.id, params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if count := fmt.Sprint(evidenceProperty(t, results, tt.field)); count != fmt.Sprint(tt.count) {
				t.Fatalf("%s count want %d got %s", tt.field, tt.count, count)
			}
		})
	}
}
