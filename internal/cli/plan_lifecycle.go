package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/fleeto/readyspec/internal/planstore"
	"github.com/fleeto/readyspec/internal/safeyaml"
	"github.com/fleeto/readyspec/internal/workspace"
	"github.com/spf13/cobra"
)

func newPlanLifecycleCommands(workspacePath func() string, setExitCode func(int)) []*cobra.Command {
	open := func() (*workspace.Store, func(), *planstore.Store, error) {
		dir, err := workspaceDirectory(workspacePath())
		if err != nil {
			return nil, nil, nil, err
		}
		ws, err := workspace.Open(dir)
		if err != nil {
			return nil, nil, nil, err
		}
		lease, err := ws.TryLock()
		if err != nil {
			_ = ws.Close()
			return nil, nil, nil, err
		}
		store, err := planstore.New(ws)
		if err != nil {
			_ = lease.Close()
			_ = ws.Close()
			return nil, nil, nil, err
		}
		return ws, func() { _ = lease.Close(); _ = ws.Close() }, store, nil
	}
	list := &cobra.Command{Use: "list", Short: "List persisted plan releases and imports", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if data, err := localRunRequest(cmd.Context(), workspacePath(), "GET", "/api/v1/plans", nil); err == nil {
			_, writeErr := cmd.OutOrStdout().Write(data)
			return writeErr
		} else if isLocalServiceResponse(err) {
			return err
		}
		ws, closeStore, store, err := open()
		if err != nil {
			setExitCode(13)
			return err
		}
		defer closeStore()
		items, err := store.List()
		if err != nil {
			return err
		}
		all := make([]any, 0, len(items))
		for _, item := range items {
			all = append(all, item)
		}
		var drafts map[string]struct {
			Key       string         `json:"key"`
			Document  map[string]any `json:"document"`
			UpdatedAt string         `json:"updated_at"`
		}
		if err := ws.ReadJSON("plans/drafts/web-index.json", &drafts); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("PLAN_STORE_ERROR")
		}
		for _, draft := range drafts {
			all = append(all, map[string]any{"key": draft.Key, "kind": "draft", "document": draft.Document, "updated_at": draft.UpdatedAt})
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"items": all})
	}}
	release := &cobra.Command{Use: "release <draft-file>", Short: "Validate and publish a DRAFT snapshot", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("PLAN_ERROR: cannot read plan file")
		}
		doc, err := safeyaml.Parse(strings.NewReader(string(raw)))
		if err != nil {
			return err
		}
		if payload, encodeErr := json.Marshal(map[string]string{"source": string(raw)}); encodeErr == nil {
			if draftData, requestErr := localRunRequest(cmd.Context(), workspacePath(), "POST", "/api/v1/plans", payload); requestErr == nil {
				var saved struct {
					DraftKey string `json:"draft_key"`
					Revision string `json:"revision"`
				}
				if json.Unmarshal(draftData, &saved) == nil && saved.DraftKey != "" && saved.Revision != "" {
					releasePayload, _ := json.Marshal(map[string]string{"expected_revision": saved.Revision})
					published, publishErr := localRunRequest(cmd.Context(), workspacePath(), "POST", "/api/v1/plans/"+url.PathEscape(saved.DraftKey)+"/release", releasePayload)
					if publishErr == nil {
						_, writeErr := cmd.OutOrStdout().Write(published)
						return writeErr
					} else if isLocalServiceResponse(publishErr) {
						return publishErr
					}
				}
			} else if isLocalServiceResponse(requestErr) {
				return requestErr
			}
		}
		_, closeStore, store, err := open()
		if err != nil {
			setExitCode(13)
			return err
		}
		defer closeStore()
		record, err := store.Release(doc, "cli-"+planKey(doc), "cli-source")
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(record)
	}}
	archive := &cobra.Command{Use: "archive <record-key>", Short: "Deactivate a published plan version", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if data, err := localRunRequest(cmd.Context(), workspacePath(), "GET", "/api/v1/plans/"+url.PathEscape(args[0]), nil); err == nil {
			var record struct {
				Revision string `json:"revision"`
			}
			if json.Unmarshal(data, &record) == nil && record.Revision != "" {
				payload, _ := json.Marshal(map[string]string{"expected_revision": record.Revision})
				archived, archiveErr := localRunRequest(cmd.Context(), workspacePath(), "POST", "/api/v1/plans/"+url.PathEscape(args[0])+"/archive", payload)
				if archiveErr == nil {
					_, writeErr := cmd.OutOrStdout().Write(archived)
					return writeErr
				} else if isLocalServiceResponse(archiveErr) {
					return archiveErr
				}
			}
		} else if isLocalServiceResponse(err) {
			return err
		}
		_, closeStore, store, err := open()
		if err != nil {
			setExitCode(13)
			return err
		}
		defer closeStore()
		record, err := store.Get(args[0])
		if err != nil {
			return err
		}
		archived, err := store.Archive(record.RecordKey, record.Revision)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(archived)
	}}
	importPlan := &cobra.Command{Use: "import <plan-file>", Short: "Strictly import a released or archived plan", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("PLAN_ERROR: cannot read plan file")
		}
		doc, err := safeyaml.Parse(strings.NewReader(string(raw)))
		if err != nil {
			return err
		}
		if payload, encodeErr := json.Marshal(map[string]string{"source": string(raw)}); encodeErr == nil {
			if data, requestErr := localRunRequest(cmd.Context(), workspacePath(), "POST", "/api/v1/plans/import", payload); requestErr == nil {
				_, writeErr := cmd.OutOrStdout().Write(data)
				return writeErr
			} else if isLocalServiceResponse(requestErr) {
				return requestErr
			}
		}
		_, closeStore, store, err := open()
		if err != nil {
			setExitCode(13)
			return err
		}
		defer closeStore()
		record, created, err := store.Import(doc)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"record": record, "created": created})
	}}
	export := &cobra.Command{Use: "export <record-key> <output-file>", Short: "Export a persisted plan snapshot", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if data, err := localRunRequest(cmd.Context(), workspacePath(), "GET", "/api/v1/plans/"+url.PathEscape(args[0])+"/export", nil); err == nil {
			if err := os.WriteFile(args[1], data, 0600); err != nil {
				return fmt.Errorf("PLAN_EXPORT_ERROR")
			}
			return nil
		} else if isLocalServiceResponse(err) {
			return err
		}
		_, closeStore, store, err := open()
		if err != nil {
			setExitCode(13)
			return err
		}
		defer closeStore()
		record, err := store.Get(args[0])
		if err != nil {
			return err
		}
		doc := record.ReleaseSnapshot
		if record.ArchiveSnapshot != nil {
			doc = record.ArchiveSnapshot
		}
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(args[1], append(raw, '\n'), 0600); err != nil {
			return fmt.Errorf("PLAN_EXPORT_ERROR")
		}
		return nil
	}}
	return []*cobra.Command{list, release, archive, importPlan, export}
}

func planKey(document map[string]any) string {
	raw, _ := json.Marshal(document)
	return fmt.Sprintf("%x", raw)
}
