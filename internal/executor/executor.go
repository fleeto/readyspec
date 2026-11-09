// Package executor defines the shared preflight and per-host execution contract.
package executor

import (
	"context"
	"fmt"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/preview"
	"github.com/fleeto/readyspec/internal/report"
)

type FailureClass string

const (
	FailureAuth         FailureClass = "AUTH_ERROR"
	FailureUnverifiable FailureClass = "PREFLIGHT_UNVERIFIABLE"
	FailureSudoUnknown  FailureClass = "SUDO_UNVERIFIABLE"
	FailureNetwork      FailureClass = "NETWORK_ERROR"
	FailureIdentity     FailureClass = "IDENTITY_ERROR"
	FailureExecution    FailureClass = "EXECUTION_ERROR"
	FailureHostTimeout  FailureClass = "HOST_TIMEOUT"
)

type HostInput struct {
	Inventory            inventory.Host
	Roles                []string
	AuthMethod           string
	PublicKeyFingerprint string
}

type PreflightRequest struct {
	RunID     string
	Plan      *plan.Plan
	Config    preview.Input
	Host      HostInput
	Artifacts compiler.ExecutionArtifacts
	Journal   Journal
}

type HostFacts struct {
	MachineID               string
	BootID                  string
	HostKey                 string
	Arch                    string
	UID                     int
	Root                    bool
	OwnerToken              string
	WorkspaceBase           string
	PrivilegedWorkspaceBase string
}

type PreflightResult struct {
	Facts   HostFacts
	Failure *Failure
}

type Failure struct {
	Class   FailureClass
	Phase   string
	Message string
}

type ExecuteRequest struct {
	RunID     string
	Plan      *plan.Plan
	Config    preview.Input
	Host      HostInput
	Facts     HostFacts
	Artifacts compiler.ExecutionArtifacts
}

type HostOutcome struct {
	Results []compiler.ItemResult
	Failure *Failure
	Cleanup []report.CleanupItem
	Modes   map[string]bool
}

type Executor interface {
	Preflight(context.Context, PreflightRequest) PreflightResult
	Execute(context.Context, ExecuteRequest, Journal) HostOutcome
}

type Journal interface {
	Phase(context.Context, string, string, string) error
	ExecutionStarted(context.Context, string, string, string) error
	CleanupRegistered(context.Context, string, report.CleanupItem) error
	CleanupUpdated(context.Context, string, report.CleanupItem) error
}

type Profiles struct {
	Hosts map[string]Profile `json:"hosts"`
}

type Profile struct {
	Preflight string            `json:"preflight"`
	MachineID string            `json:"machine_id"`
	BootID    string            `json:"boot_id"`
	HostKey   string            `json:"host_key_fingerprint"`
	Arch      string            `json:"arch"`
	UID       int               `json:"uid"`
	Results   map[string]string `json:"results"`
}

func ValidateStatus(status string) error {
	switch status {
	case "PASS", "FAIL", "ERROR", "UNSUPPORTED":
		return nil
	default:
		return fmt.Errorf("invalid simulator result status")
	}
}
