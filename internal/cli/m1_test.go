package cli_test

import (
	"bytes"
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
