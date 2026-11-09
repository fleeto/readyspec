package cli

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/runmanager"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/webui"
	"github.com/fleeto/readyspec/internal/workspace"
)

func TestDelegatedSubmissionPreservesValidatedPlanDocument(t *testing.T) {
	const source = `schema_version: readyspec.plan/v1
plan_id: delegated-document-test
name: Delegated document test
description: ''
software_name: demo
software_version: '1'
version: '1.0.0'
state: DRAFT
topology_policy: {allowed_arch: [x86_64]}
roles: [{role_id: app, name: App, min_nodes: 1, max_nodes: 1, allow_colocation: false, items: []}]
embedded_templates: []
template_lock: {}
`
	p, err := plan.Load(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	body, err := delegatedSubmission(p, runstore.RunConfig{}, "node=127.0.0.1", "quick", "delegate-document-test", nil, nil, nil, nil, nil, executor.Profiles{})
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	delegated, err := plan.Load(strings.NewReader(request.Plan))
	if err != nil {
		t.Fatalf("delegated document must preserve the validated digest: %v", err)
	}
	if delegated.Digest != p.Digest {
		t.Fatalf("digest changed during delegation: got %s want %s", delegated.Digest, p.Digest)
	}
}

func TestDelegateRunVerifiesInstanceAndWaitsForArchivedReport(t *testing.T) {
	const source = `schema_version: readyspec.plan/v1
plan_id: delegated-test
name: Delegated test
software_name: demo
software_version: '1'
version: '1.0.0'
state: DRAFT
topology_policy: {allowed_arch: [x86_64]}
roles: [{role_id: app, name: App, min_nodes: 1, max_nodes: 1, allow_colocation: false, items: []}]
embedded_templates: []
template_lock: {}
`
	p, err := plan.Load(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	lease, err := ws.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	store, err := runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	profiles := executor.Profiles{Hosts: map[string]executor.Profile{
		"node": {Preflight: "READY", MachineID: "machine-1", BootID: "boot-1", HostKey: "SHA256:fixture", Arch: "x86_64", UID: 1000},
	}}
	manager, err := runmanager.New(store, executor.NewNativeSSH())
	if err != nil {
		t.Fatal(err)
	}
	const token = "delegation-local-token-01234567890123456789"
	const instanceID = "server-instance-test"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := webui.New(webui.Options{Address: listener.Addr().String(), Token: "web-login-token-01234567890123456789", LocalToken: token, ServerID: instanceID, Store: ws, Runs: manager, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewUnstartedServer(server)
	httpServer.Listener = listener
	httpServer.Start()
	defer httpServer.Close()
	address := strings.TrimPrefix(httpServer.URL, "http://")
	if err := ws.WriteJSON("runtime/server.json", serverInstance{ServerID: instanceID, Address: address, LocalToken: token, PID: 123, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); _ = manager.Run(workerCtx) }()
	defer func() { cancelWorker(); <-workerDone }()

	hosts := []inventory.Host{{ID: "node", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	config := runstore.RunConfig{PlanDigest: p.Digest, Mode: "TRIAL", Scope: "FULL", Backend: "mock_simulator", Inventory: hosts, RoleMap: map[string][]string{"app": {"node"}}, Auth: runstore.AuthIdentity{Method: "offline"}, Simulator: profiles}
	body, err := delegatedSubmission(p, config, "node=127.0.0.1", "quick", "delegate-test-key", nil, nil, nil, nil, nil, profiles)
	if err != nil {
		t.Fatal(err)
	}
	result, err := delegateRun(context.Background(), ws, body, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunStatus != "COMPLETED" || result.Verdict != "UNKNOWN" || result.ExecutorBackend != "mock_simulator" {
		t.Fatalf("unexpected delegated report: %+v", result)
	}
	var persisted serverInstance
	if err := ws.ReadJSON("runtime/server.json", &persisted); err != nil || persisted.ServerID != instanceID {
		t.Fatalf("discovery record changed: %+v %v", persisted, err)
	}
}
