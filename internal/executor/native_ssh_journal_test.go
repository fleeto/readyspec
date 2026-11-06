package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/fleeto/readyspec/internal/report"
)

type journalProbe struct {
	markerErr error
	phaseErr  error
	calls     []string
	started   map[string]bool
}

func (j *journalProbe) Phase(_ context.Context, _, _, phase string) error {
	j.calls = append(j.calls, "phase:"+phase)
	return j.phaseErr
}
func (j *journalProbe) ExecutionStarted(_ context.Context, _, _, mode string) error {
	j.calls = append(j.calls, "marker:"+mode)
	if j.started == nil {
		j.started = map[string]bool{}
	}
	if j.started[mode] {
		return errors.New("execution marker already exists")
	}
	j.started[mode] = true
	return j.markerErr
}
func (*journalProbe) CleanupRegistered(context.Context, string, report.CleanupItem) error { return nil }
func (*journalProbe) CleanupUpdated(context.Context, string, report.CleanupItem) error    { return nil }

func TestExecutionMarkerFailureStopsBeforeRunningPhase(t *testing.T) {
	probe := &journalProbe{markerErr: errors.New("disk full")}
	err := markExecutionStarted(context.Background(), probe, "run-1", "host-1", "STANDARD")
	if err == nil {
		t.Fatal("execution was allowed after marker persistence failed")
	}
	if want := []string{"marker:STANDARD"}; !reflect.DeepEqual(probe.calls, want) {
		t.Fatalf("journal calls=%v want=%v", probe.calls, want)
	}
}

func TestExecutionMarkerIsDurablyRequestedBeforeRunningPhase(t *testing.T) {
	probe := &journalProbe{}
	if err := markExecutionStarted(context.Background(), probe, "run-1", "host-1", "PRIVILEGED"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"marker:PRIVILEGED", "phase:RUNNING_PRIVILEGED"}; !reflect.DeepEqual(probe.calls, want) {
		t.Fatalf("journal calls=%v want=%v", probe.calls, want)
	}
}

func TestSudoPolicyFailureClassificationSeparatesDenialFromUnverifiable(t *testing.T) {
	tests := []struct {
		name       string
		queryErr   error
		cleanupErr error
		output     string
		want       FailureClass
	}{
		{name: "allowed and timestamp cleared", want: ""},
		{name: "explicit denial", queryErr: errors.New("sudo: user is not allowed to execute this command"), want: FailureAuth},
		{name: "sudo-rs explicit denial", queryErr: errors.New("sudo: Sorry, user tester may not run sudo on host."), want: FailureAuth},
		{name: "missing password", queryErr: errors.New("sudo: a password is required"), want: FailureAuth},
		{name: "sudo-rs requires interactive authentication", queryErr: errors.New("sudo: interactive authentication is required"), want: FailureAuth},
		{name: "unrecognized policy response", queryErr: errors.New("sudo exited with status 1"), output: "policy output could not be parsed", want: FailureSudoUnknown},
		{name: "timestamp clear failure", cleanupErr: errors.New("sudo -k failed"), want: FailureSudoUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifySudoPolicyResult(tt.queryErr, tt.cleanupErr, []byte(tt.output)); got != tt.want {
				t.Fatalf("classifySudoPolicyResult()=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestCleanupNeverTerminatesProcessesWithoutExplicitConfirmation(t *testing.T) {
	for _, tt := range []struct {
		name      string
		pids      []string
		confirmed bool
		want      bool
	}{
		{name: "no process", want: true},
		{name: "live process without confirmation", pids: []string{"123"}, want: false},
		{name: "live process with confirmation", pids: []string{"123"}, confirmed: true, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := mayTerminateCleanupProcesses(tt.pids, tt.confirmed); got != tt.want {
				t.Fatalf("mayTerminateCleanupProcesses()=%v want=%v", got, tt.want)
			}
		})
	}
}
