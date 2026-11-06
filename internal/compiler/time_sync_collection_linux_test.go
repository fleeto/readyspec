//go:build linux

package compiler_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTimeSyncCollectorValidatesQueryStates(t *testing.T) {
	for _, tt := range []struct {
		name, sync, state, status string
		syncRC, serviceRC         int
		expected                  bool
	}{
		{"chrony_active", "yes\n", "active\n", "PASS", 0, 0, true},
		{"unsynchronized", "no\n", "active\n", "FAIL", 0, 0, true},
		{"expected_unsynchronized", "no\n", "active\n", "PASS", 0, 0, false},
		{"inactive", "yes\n", "inactive\n", "FAIL", 0, 3, true},
		{"not_found", "yes\n", "unknown\n", "FAIL", 0, 4, true},
		{"missing_unit_inactive", "yes\n", "inactive\n", "FAIL", 0, 4, true},
		{"failed_service", "yes\n", "failed\n", "FAIL", 0, 3, true},
		{"activating", "yes\n", "activating\n", "FAIL", 0, 3, true},
		{"reloading", "yes\n", "reloading\n", "PASS", 0, 0, true},
		{"refreshing", "yes\n", "refreshing\n", "PASS", 0, 0, true},
		{"private_diagnostic", "yes\n", "private-time-diagnostic\n", "ERROR", 0, 2, true},
		{"inactive_with_success_exit", "yes\n", "inactive\n", "ERROR", 0, 0, true},
		{"active_with_failure_exit", "yes\n", "active\n", "ERROR", 0, 3, true},
		{"garbage_service", "yes\n", "mystery\n", "ERROR", 0, 0, true},
		{"empty_service", "yes\n", "", "ERROR", 0, 0, true},
		{"multiline_service", "yes\n", "active\nactive\n", "ERROR", 0, 0, true},
		{"extra_blank_sync", "yes\n\n", "active\n", "ERROR", 0, 0, true},
		{"nul_sync", "y\x00es\n", "active\n", "ERROR", 0, 0, true},
		{"invalid_sync", "maybe\n", "active\n", "ERROR", 0, 0, true},
		{"sync_query_failure", "yes\n", "active\n", "ERROR", 2, 0, true},
		{"service_query_failure", "yes\n", "active\n", "ERROR", 0, 2, true},
		{"missing_timedatectl", "", "active\n", "ERROR", 127, 0, true},
		{"missing_systemctl", "yes\n", "", "ERROR", 0, 127, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "time-sync", tt.sync)
			fixtureFile(t, root, "time-service", tt.state)
			fixtureTool(t, root, "tools/timedatectl", fmt.Sprintf(`[ "$*" = 'show -p NTPSynchronized --value' ] || exit 99; cat /time-sync; exit %d`, tt.syncRC))
			fixtureTool(t, root, "tools/systemctl", fmt.Sprintf(`[ "$*" = 'is-active chronyd' ] || exit 99; cat /time-service; exit %d`, tt.serviceRC))
			results := collectInSandbox(t, root, collectionArtifact(t, "R05", map[string]any{"allowed_services": []any{"chronyd"}, "synchronized": tt.expected}))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if tt.syncRC != 0 && evidenceProperty(t, results, "error") != "SYNC_QUERY_FAILED" {
				t.Fatal("sync query failure classification missing")
			}
			if tt.syncRC == 0 && tt.serviceRC != 0 && tt.serviceRC != 3 && tt.serviceRC != 4 && evidenceProperty(t, results, "error") != "SERVICE_QUERY_FAILED" {
				t.Fatal("service query failure classification missing")
			}
			raw, _ := json.Marshal(results)
			if strings.Contains(string(raw), "private-time-diagnostic") {
				t.Fatal("raw tool diagnostics leaked into evidence")
			}
			if tt.status != "ERROR" {
				if actual := evidenceProperty(t, results, "synchronized"); actual != (tt.sync == "yes\n") {
					t.Fatalf("observed synchronization lost: %v", actual)
				}
				_ = evidenceProperty(t, results, "service_queries")
			}
		})
	}
}

func TestTimeSyncCollectorRejectsFailureAfterAnActiveService(t *testing.T) {
	root := collectionRoot(t)
	fixtureTool(t, root, "tools/timedatectl", `printf 'yes\n'`)
	fixtureTool(t, root, "tools/systemctl", `case "$*" in 'is-active chronyd') printf 'active\n';; 'is-active ntpd') exit 2;; *) exit 99;; esac`)
	results := collectInSandbox(t, root, collectionArtifact(t, "R05", map[string]any{"allowed_services": []any{"chronyd", "ntpd"}}))
	if len(results) != 1 || results[0].Status != "ERROR" {
		t.Fatalf("later query failure must not be hidden: %+v", results)
	}
}

func TestTimeSyncCollectorChecksEveryAllowedService(t *testing.T) {
	for _, name := range []string{"chronyd", "chrony", "systemd-timesyncd", "ntpd", "ntp"} {
		t.Run(name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureTool(t, root, "tools/timedatectl", `[ "$*" = 'show -p NTPSynchronized --value' ]; printf 'yes\n'`)
			fixtureTool(t, root, "tools/systemctl", `case "$*" in 'is-active `+name+`') printf 'active\n'; exit 0;; 'is-active chronyd'|'is-active chrony'|'is-active systemd-timesyncd'|'is-active ntpd'|'is-active ntp') printf 'inactive\n'; exit 3;; *) exit 99;; esac`)
			results := collectInSandbox(t, root, collectionArtifact(t, "R05", map[string]any{"allowed_services": []any{"chronyd", "chrony", "systemd-timesyncd", "ntpd", "ntp"}}))
			if len(results) != 1 || results[0].Status != "PASS" {
				t.Fatalf("allowed active service must pass: %+v", results)
			}
			if actual := fmt.Sprint(evidenceProperty(t, results, "active_services")); actual != "["+name+"]" {
				t.Fatalf("want active %s got %s", name, actual)
			}
			for _, entry := range results[0].Evidence {
				if entry.Source != "timedatectl and systemctl" || entry.StartTime == "" || entry.EndTime == "" {
					t.Fatal("time source or observation times lost")
				}
			}
		})
	}
}
