package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/workspace"
)

func main() {
	root := os.Getenv("READYSPEC_WORKSPACE")
	if root == "" {
		fatal("READYSPEC_WORKSPACE is required")
	}
	store, err := workspace.Open(root)
	if err != nil {
		fatal("open workspace: %v", err)
	}
	defer store.Close()
	lease, err := store.TryLock()
	if err != nil {
		fatal("lock workspace: %v", err)
	}
	defer lease.Close()
	runs, err := runstore.Open(store)
	if err != nil {
		fatal("open run store: %v", err)
	}
	value, err := plan.Load(strings.NewReader(`schema_version: readyspec.plan/v1
plan_id: cleanup-browser-fixture
name: Cleanup browser fixture
software_name: fixture
software_version: '1'
version: '1.0.0'
state: DRAFT
topology_policy: {allowed_arch: [x86_64]}
roles: [{role_id: app, name: App, min_nodes: 1, max_nodes: 1, allow_colocation: false, items: []}]
embedded_templates: []
template_lock: {}
`))
	if err != nil {
		fatal("load fixture plan: %v", err)
	}
	accepted, _, err := runs.Accept(runstore.Request{
		IdempotencyKey: "cleanup-browser-fixture",
		Plan:           value,
		Config: runstore.RunConfig{
			PlanDigest: value.Digest,
			Mode:       "TRIAL",
			Scope:      "FULL",
			Backend:    "native_ssh",
			Inventory:  []inventory.Host{{ID: "cleanup-node", Address: "127.0.0.1", Port: 22, Username: "fixture"}},
			RoleMap:    map[string][]string{"app": {"cleanup-node"}},
			Auth:       runstore.AuthIdentity{Method: "password"},
		},
	})
	if err != nil {
		fatal("accept fixture run: %v", err)
	}
	if err := runs.CleanupRegistered(context.Background(), accepted.Acceptance.RunID, report.CleanupItem{
		CleanupID:  "cleanup-browser-item",
		HostID:     "cleanup-node",
		MachineID:  "fixture-machine",
		BootID:     "fixture-boot",
		Status:     "PENDING",
		RemotePath: "/tmp/readyspec-cleanup-browser-fixture",
		OwnerToken: "0123456789abcdef0123456789abcdef",
	}); err != nil {
		fatal("register fixture cleanup: %v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
