package cli_test

import (
	"bytes"
	"encoding/json"
	"github.com/fleeto/readyspec/internal/cli"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateListExposesAvailableBuiltinSnapshots(t *testing.T) {
	var out, stderr bytes.Buffer
	code := cli.Execute([]string{"template", "list", "--json"}, &out, &stderr)
	if code != 0 || !strings.Contains(out.String(), `"template_id":"S01"`) {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), stderr.String())
	}
}

func TestRunPreviewOnlyProducesStaticResults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.yaml")
	source := `schema_version: readyspec.plan/v1
plan_id: preview
name: Preview
software_name: fixture
software_version: '1'
version: '1'
state: DRAFT
topology_policy: {allowed_arch: [x86_64]}
roles: [{role_id: app, name: App, min_nodes: 1, max_nodes: null, allow_colocation: false, items: []}]
embedded_templates: []
template_lock: {}
`
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code := cli.Execute([]string{"run", "preview", "--plan", path, "--inventory", "node=192.0.2.1", "--role-map", "app=node", "--json"}, &out, &stderr)
	if code != 0 || !strings.Contains(out.String(), `"network_accessed":false`) || !strings.Contains(out.String(), `"host_count":1`) {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), stderr.String())
	}
}

func TestDirectRunSubmitKeepsProgressOnStderrAndReportOnStdout(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.yaml")
	planSource := `schema_version: readyspec.plan/v1
plan_id: direct-progress
name: Direct progress
software_name: fixture
software_version: '1'
version: '1'
state: DRAFT
topology_policy: {allowed_arch: [x86_64]}
roles: [{role_id: app, name: App, min_nodes: 1, max_nodes: 1, allow_colocation: false, items: []}]
embedded_templates: []
template_lock: {}
`
	if err := os.WriteFile(planPath, []byte(planSource), 0600); err != nil {
		t.Fatal(err)
	}
	profilesPath := filepath.Join(dir, "profiles.json")
	profiles := `{"hosts":{"node":{"preflight":"READY","machine_id":"machine-1","boot_id":"boot-1","host_key_fingerprint":"SHA256:test","arch":"x86_64","uid":1000}}}`
	if err := os.WriteFile(profilesPath, []byte(profiles), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	code := cli.Execute([]string{
		"--workspace", filepath.Join(dir, "workspace"), "run", "submit", "--plan", planPath,
		"--inventory", "node=127.0.0.1", "--role-map", "app=node", "--mode", "TRIAL",
		"--backend", "mock_simulator", "--profiles", profilesPath,
	}, &out, &stderr)
	if code != 11 {
		t.Fatalf("exit code=%d, want UNKNOWN result code 11; stderr=%s", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result["run_status"] != "COMPLETED" {
		t.Fatalf("stdout does not contain the authoritative JSON report: decode=%v body=%q", err, out.String())
	}
	if strings.Contains(out.String(), "Accepted Run") || !strings.Contains(stderr.String(), "Accepted Run ") || !strings.Contains(stderr.String(), "PREFLIGHT") {
		t.Fatalf("progress/report streams are not separated: stdout=%q stderr=%q", out.String(), stderr.String())
	}
}
