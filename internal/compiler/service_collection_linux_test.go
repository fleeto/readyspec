//go:build linux

package compiler_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestServiceCollectorValidatesIndependentStates(t *testing.T) {
	for _, tt := range []struct {
		name, load, active, unit, status string
		running, enabled                 bool
	}{
		{"running_enabled", "loaded", "active", "enabled", "PASS", true, true},
		{"running_disabled", "loaded", "active", "disabled", "PASS", true, false},
		{"stopped_enabled", "loaded", "inactive", "enabled", "PASS", false, true},
		{"stopped_disabled", "loaded", "inactive", "disabled", "PASS", false, false},
		{"runtime_enabled", "loaded", "active", "enabled-runtime", "PASS", true, true},
		{"static_not_enabled", "loaded", "active", "static", "PASS", true, false},
		{"indirect_not_enabled", "loaded", "inactive", "indirect", "PASS", false, false},
		{"linked_not_enabled", "loaded", "inactive", "linked", "PASS", false, false},
		{"transient_not_enabled", "loaded", "active", "transient", "PASS", true, false},
		{"generated_not_enabled", "loaded", "active", "generated", "PASS", true, false},
		{"masked", "masked", "inactive", "masked", "PASS", false, false},
		{"reloading_running", "loaded", "reloading", "enabled", "PASS", true, true},
		{"refreshing_running", "loaded", "refreshing", "enabled", "PASS", true, true},
		{"failed_not_running", "loaded", "failed", "disabled", "PASS", false, false},
		{"activating_not_active", "loaded", "activating", "disabled", "PASS", false, false},
		{"deactivating_not_active", "loaded", "deactivating", "disabled", "PASS", false, false},
		{"linked_runtime_not_enabled", "loaded", "inactive", "linked-runtime", "PASS", false, false},
		{"alias_not_enabled", "loaded", "inactive", "alias", "PASS", false, false},
		{"running_mismatch", "loaded", "inactive", "enabled", "FAIL", true, true},
		{"enabled_mismatch", "loaded", "active", "static", "FAIL", true, true},
		{"not_installed", "not-found", "inactive", "", "FAIL", false, false},
		{"unknown_activity", "loaded", "mystery", "disabled", "ERROR", false, false},
		{"unknown_unit_state", "loaded", "inactive", "mystery", "ERROR", false, false},
		{"bad_unit_file", "loaded", "inactive", "bad", "ERROR", false, false},
		{"bad_load_state", "error", "inactive", "disabled", "ERROR", false, false},
		{"empty_unit_state", "loaded", "inactive", "", "ERROR", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "service-state", fmt.Sprintf("LoadState=%s\nActiveState=%s\nUnitFileState=%s\n", tt.load, tt.active, tt.unit))
			fixtureTool(t, root, "tools/systemctl", `[ "$*" = 'show --no-pager --all -p LoadState -p ActiveState -p UnitFileState -- app.service' ]; cat /service-state`)
			results := collectInSandbox(t, root, collectionArtifact(t, "R01", map[string]any{"name": "app.service", "running": tt.running, "enabled": tt.enabled}))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" {
				t.Fatal("state parsing must run after a successful query")
			}
			for _, entry := range results[0].Evidence {
				if entry.Source != "systemctl show" || entry.StartTime == "" || entry.EndTime == "" {
					t.Fatal("service source or collection times lost")
				}
			}
			if tt.status != "ERROR" {
				if evidenceProperty(t, results, "active_state") != tt.active || evidenceProperty(t, results, "unit_file_state") != tt.unit {
					t.Fatal("independent unit state evidence lost")
				}
			}
		})
	}
}

func TestServiceCollectorReportsQueryFailures(t *testing.T) {
	for _, rc := range []int{2, 127} {
		t.Run(fmt.Sprintf("exit_%d", rc), func(t *testing.T) {
			root := collectionRoot(t)
			fixtureTool(t, root, "tools/systemctl", fmt.Sprintf(`printf 'LoadState=not-found\nActiveState=inactive\nUnitFileState=\n'; exit %d`, rc))
			results := collectInSandbox(t, root, collectionArtifact(t, "R01", map[string]any{"name": "app.service", "running": false, "enabled": false}))
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("failed query must override apparent absence: %+v", results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != fmt.Sprint(rc) {
				t.Fatal("query exit code lost")
			}
		})
	}
}

func TestServiceCollectorRejectsDamagedQueryOutputs(t *testing.T) {
	for name, output := range map[string]string{
		"empty":              "",
		"missing_activity":   "LoadState=loaded\nUnitFileState=disabled\n",
		"missing_unit_state": "LoadState=loaded\nActiveState=inactive\n",
		"duplicate_activity": "LoadState=loaded\nActiveState=active\nActiveState=inactive\nUnitFileState=disabled\n",
		"duplicate_load":     "LoadState=loaded\nLoadState=not-found\nActiveState=inactive\nUnitFileState=disabled\n",
		"malformed_line":     "LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\nbroken\n",
		"nul":                "LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\x00\n",
		"diagnostic":         "Failed to connect to bus: private-service-diagnostic\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "service-state", output)
			fixtureTool(t, root, "tools/systemctl", `cat /service-state`)
			results := collectInSandbox(t, root, collectionArtifact(t, "R01", map[string]any{"name": "app.service", "running": false, "enabled": false}))
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("corrupt query cannot prove stopped/disabled: %+v", results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "exit-status")) != "0" || evidenceProperty(t, results, "error") != "INVALID_UNIT_OBSERVATION" {
				t.Fatal("query content was not rejected by the state parser")
			}
			raw, _ := json.Marshal(results)
			if strings.Contains(string(raw), "private-service-diagnostic") {
				t.Fatal("raw service diagnostics leaked")
			}
		})
	}
}
