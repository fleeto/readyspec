// Package publicview builds safe copies of Run data for external interfaces.
package publicview

import (
	"encoding/json"

	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/topology"
)

type Run struct {
	RunID           string            `json:"run_id"`
	Status          string            `json:"status"`
	RecoveryError   string            `json:"recovery_error,omitempty"`
	RecoveryWarning string            `json:"recovery_warning,omitempty"`
	Events          []runstore.Event  `json:"events"`
	Report          *report.RunReport `json:"report,omitempty"`
}

func RunValue(value runstore.Run) Run {
	events := make([]runstore.Event, len(value.Events))
	for i, event := range value.Events {
		events[i] = EventValue(event)
	}
	var resultReport *report.RunReport
	if value.Report != nil {
		copy := *value.Report
		copy.CleanupItems = append([]report.CleanupItem(nil), value.Report.CleanupItems...)
		for i := range copy.CleanupItems {
			copy.CleanupItems[i].OwnerToken = ""
		}
		resultReport = &copy
	}
	return Run{RunID: value.Acceptance.RunID, Status: value.Status, RecoveryError: value.RecoveryError, RecoveryWarning: value.RecoveryWarning, Events: events, Report: resultReport}
}

func EventValue(value runstore.Event) runstore.Event {
	switch value.Kind {
	case "HOST_OUTCOME":
		var result report.HostResult
		if json.Unmarshal(value.Data, &result) != nil || result.HostID != value.HostID {
			value.Data = nil
			return value
		}
		value.Data, _ = json.Marshal(result)
		return value
	case "TOPOLOGY_RESULTS":
		var results []topology.Result
		if json.Unmarshal(value.Data, &results) != nil {
			value.Data = nil
			return value
		}
		value.Data, _ = json.Marshal(results)
		return value
	case "CLEANUP_REGISTERED", "CLEANUP_UPDATED":
		var item report.CleanupItem
		if json.Unmarshal(value.Data, &item) != nil || item.CleanupID == "" || item.HostID != value.HostID {
			value.Data = nil
			return value
		}
		item.OwnerToken = ""
		value.Data, _ = json.Marshal(item)
		return value
	default:
		value.Data = nil
		return value
	}
}
