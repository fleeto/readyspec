package cli

import (
	"encoding/json"
	"fmt"

	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/workspace"
	"github.com/spf13/cobra"
)

func newReportCommand(workspacePath func() string, setExitCode func(int)) *cobra.Command {
	command := &cobra.Command{Use: "report", Short: "Read archived Run reports"}
	var jsonOutput bool
	list := &cobra.Command{
		Use: "list", Short: "List accepted Runs", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, closeStore, err := openRunStore(workspacePath())
			if err != nil {
				setExitCode(13)
				return err
			}
			defer closeStore()
			runs := store.List()
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(runs)
			}
			for _, run := range runs {
				verdict := "-"
				if run.Report != nil {
					verdict = run.Report.Verdict
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", run.Acceptance.RunID, run.Status, verdict); err != nil {
					return err
				}
			}
			return nil
		},
	}
	list.Flags().BoolVar(&jsonOutput, "json", false, "Output Runs as JSON")
	show := &cobra.Command{
		Use: "show <run-id>", Short: "Print an archived Run report", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, closeStore, err := openRunStore(workspacePath())
			if err != nil {
				setExitCode(13)
				return err
			}
			defer closeStore()
			run, err := store.Get(args[0])
			if err != nil {
				setExitCode(13)
				return fmt.Errorf("RUN_NOT_FOUND")
			}
			if run.Report == nil {
				setExitCode(13)
				return fmt.Errorf("RUN_REPORT_NOT_READY")
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(run.Report)
		},
	}
	command.AddCommand(list, show)
	return command
}

func openRunStore(path string) (*runstore.Store, func(), error) {
	dir, err := workspaceDirectory(path)
	if err != nil {
		return nil, nil, err
	}
	ws, err := workspace.Open(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("INFRA_ERROR: cannot open workspace")
	}
	lease, err := ws.TryLock()
	if err != nil {
		_ = ws.Close()
		return nil, nil, err
	}
	store, err := runstore.Open(ws)
	if err != nil {
		_ = lease.Close()
		_ = ws.Close()
		return nil, nil, err
	}
	return store, func() { _ = lease.Close(); _ = ws.Close() }, nil
}
