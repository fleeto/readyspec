package executor

import (
	"context"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/report"
)

// Simulator classifies only caller-supplied offline profiles. It performs no
// local command, SSH, or network operation.
type Simulator struct{ Profiles Profiles }

func (s Simulator) Preflight(ctx context.Context, req PreflightRequest) PreflightResult {
	if req.Journal != nil {
		if err := req.Journal.Phase(ctx, req.RunID, req.Host.Inventory.ID, "SIMULATING_PREFLIGHT"); err != nil {
			return PreflightResult{Failure: &Failure{Class: FailureExecution, Phase: "JOURNAL", Message: "cannot persist simulator phase"}}
		}
	}
	if err := ctx.Err(); err != nil {
		return PreflightResult{Failure: &Failure{Class: FailureExecution, Phase: "PREFLIGHT", Message: "simulator preflight cancelled"}}
	}
	profile, ok := s.Profiles.Hosts[req.Host.Inventory.ID]
	if !ok {
		return PreflightResult{Failure: &Failure{Class: FailureUnverifiable, Phase: "PREFLIGHT", Message: "no explicit offline host profile"}}
	}
	if profile.Preflight != "READY" {
		class := FailureUnverifiable
		if profile.Preflight == "AUTH_REJECTED" {
			class = FailureAuth
		} else if profile.Preflight == "UNREACHABLE" {
			class = FailureNetwork
		}
		return PreflightResult{Failure: &Failure{Class: class, Phase: "PREFLIGHT", Message: "offline profile reports preflight failure"}}
	}
	return PreflightResult{Facts: HostFacts{
		MachineID: profile.MachineID, BootID: profile.BootID, HostKey: profile.HostKey,
		Arch: profile.Arch, UID: profile.UID, Root: profile.UID == 0,
	}}
}

func (s Simulator) Execute(ctx context.Context, req ExecuteRequest, journal Journal) HostOutcome {
	if err := ctx.Err(); err != nil {
		return HostOutcome{Failure: &Failure{Class: FailureExecution, Phase: "EXECUTE", Message: "simulator execution cancelled"}}
	}
	profile, ok := s.Profiles.Hosts[req.Host.Inventory.ID]
	if !ok {
		return HostOutcome{Failure: &Failure{Class: FailureUnverifiable, Phase: "EXECUTE", Message: "offline profile disappeared"}}
	}
	out := HostOutcome{Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
	for _, artifact := range []compiler.Artifact{req.Artifacts.Standard, req.Artifacts.Privileged} {
		if len(artifact.Items) == 0 {
			continue
		}
		if err := journal.Phase(ctx, req.RunID, req.Host.Inventory.ID, "SIMULATING_"+artifact.ExecutionMode); err != nil {
			return HostOutcome{Results: out.Results, Failure: &Failure{Class: FailureExecution, Phase: "JOURNAL", Message: "cannot persist execution phase"}, Modes: out.Modes}
		}
		if err := journal.ExecutionStarted(ctx, req.RunID, req.Host.Inventory.ID, artifact.ExecutionMode); err != nil {
			return HostOutcome{Results: out.Results, Failure: &Failure{Class: FailureExecution, Phase: "JOURNAL", Message: "cannot persist execution marker"}, Modes: out.Modes}
		}
		modeComplete := true
		for _, item := range artifact.Items {
			status, exists := profile.Results[item.ItemID]
			if !exists || ValidateStatus(status) != nil {
				modeComplete = false
				out.Results = append(out.Results, compiler.ItemResult{
					RoleID: item.RoleID, ItemID: item.ItemID, Status: "ERROR", TemplateID: item.TemplateID,
					TemplateVersion: item.TemplateVersion, ExecutionMode: item.ExecutionMode, EvidenceMode: item.EvidenceMode,
				})
				continue
			}
			out.Results = append(out.Results, compiler.ItemResult{
				RoleID: item.RoleID, ItemID: item.ItemID, Status: status, TemplateID: item.TemplateID,
				TemplateVersion: item.TemplateVersion, ExecutionMode: item.ExecutionMode, EvidenceMode: item.EvidenceMode,
				Evidence: []compiler.Evidence{{Resource: "simulator", Property: item.ItemID, Expected: "profile assertion", Actual: status, Source: "mock_profile"}},
			})
		}
		out.Modes[artifact.ExecutionMode] = modeComplete
	}
	if err := journal.Phase(ctx, req.RunID, req.Host.Inventory.ID, "FINISHED"); err != nil {
		return failedOutcome(out, "JOURNAL", "cannot persist simulator completion phase")
	}
	return out
}

var _ Executor = Simulator{}
