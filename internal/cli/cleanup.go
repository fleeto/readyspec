package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runmanager"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/workspace"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

func newCleanupCommand(workspacePath func() string, setExitCode func(int)) *cobra.Command {
	cleanup := &cobra.Command{Use: "cleanup", Short: "Resolve remote execution leftovers"}
	var jsonOutput bool
	var runID, cleanupID, authMethod, keyFile, agentSocket, ignoreReason string
	var confirmTerminate bool
	resolve := &cobra.Command{
		Use: "resolve", Short: "Reverify and resolve one archived cleanup item", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runID == "" || cleanupID == "" {
				setExitCode(12)
				return fmt.Errorf("PLAN_ERROR: --run-id and --cleanup-id are required")
			}
			credential := executor.Credential{Method: authMethod}
			if ignoreReason == "" {
				credential.SudoPassword = []byte(os.Getenv("READYSPEC_SUDO_PASSWORD"))
			}
			defer func() {
				for i := range credential.Password {
					credential.Password[i] = 0
				}
				credential.Password = nil
				credential.Signer = nil
				credential.AgentSocket = ""
				for i := range credential.SudoPassword {
					credential.SudoPassword[i] = 0
				}
				credential.SudoPassword = nil
			}()
			switch {
			case ignoreReason != "":
				// An audited ignore decision does not contact the target.
			case authMethod == "password":
				credential.Password = []byte(os.Getenv("READYSPEC_SSH_PASSWORD"))
				if len(credential.Password) == 0 {
					setExitCode(15)
					return fmt.Errorf("AUTH_ERROR: READYSPEC_SSH_PASSWORD is required")
				}
			case authMethod == "private_key":
				if keyFile == "" {
					setExitCode(15)
					return fmt.Errorf("AUTH_ERROR: --private-key is required")
				}
				key, err := readBoundedFile(keyFile)
				if err != nil {
					setExitCode(15)
					return fmt.Errorf("AUTH_ERROR: cleanup private key cannot be read")
				}
				credential.Signer, err = ssh.ParsePrivateKey(key)
				if err != nil {
					passphrase := []byte(os.Getenv("READYSPEC_SSH_KEY_PASSPHRASE"))
					credential.Signer, err = ssh.ParsePrivateKeyWithPassphrase(key, passphrase)
					for i := range passphrase {
						passphrase[i] = 0
					}
				}
				for i := range key {
					key[i] = 0
				}
				if err != nil {
					setExitCode(15)
					return fmt.Errorf("AUTH_ERROR: cleanup private key cannot be parsed")
				}
			case authMethod == "agent":
				if agentSocket == "" {
					setExitCode(15)
					return fmt.Errorf("AUTH_ERROR: --agent-socket is required")
				}
				credential.AgentSocket = agentSocket
			default:
				setExitCode(15)
				return fmt.Errorf("AUTH_ERROR: choose --auth password, private_key, or agent")
			}
			dir, err := workspaceDirectory(workspacePath())
			if err != nil {
				setExitCode(13)
				return err
			}
			ws, err := workspace.Open(dir)
			if err != nil {
				setExitCode(13)
				return fmt.Errorf("INFRA_ERROR: cannot open workspace")
			}
			defer ws.Close()
			lease, err := ws.TryLock()
			if err != nil {
				if errors.Is(err, workspace.ErrBusy) {
					setExitCode(16)
					return fmt.Errorf("WORKSPACE_BUSY")
				}
				setExitCode(13)
				return err
			}
			defer lease.Close()
			store, err := runstore.Open(ws)
			if err != nil {
				setExitCode(13)
				return err
			}
			manager, err := runmanager.New(store, executor.NewNativeSSH())
			if err != nil {
				setExitCode(13)
				return err
			}
			item, err := manager.ResolveCleanup(cmd.Context(), runID, cleanupID, credential, confirmTerminate, ignoreReason)
			if err != nil {
				if len(err.Error()) >= 10 && err.Error()[:10] == "AUTH_ERROR" {
					setExitCode(15)
				} else {
					setExitCode(13)
				}
				return err
			}
			item.OwnerToken = ""
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(item); err != nil {
				setExitCode(13)
				return err
			}
			return nil
		},
	}
	resolve.Flags().StringVar(&runID, "run-id", "", "Archived Run identifier")
	resolve.Flags().StringVar(&cleanupID, "cleanup-id", "", "Cleanup item identifier")
	resolve.Flags().StringVar(&authMethod, "auth", "", "Fresh authentication method: password, private_key, or agent")
	resolve.Flags().StringVar(&keyFile, "private-key", "", "Fresh private key file")
	resolve.Flags().StringVar(&agentSocket, "agent-socket", "", "Explicit local SSH Agent socket")
	resolve.Flags().BoolVar(&confirmTerminate, "confirm-terminate", false, "Confirm termination of associated live processes and cleanup")
	resolve.Flags().StringVar(&ignoreReason, "ignore-reason", "", "Audit reason to mark this item IGNORED")
	cleanup.AddCommand(resolve)
	list := &cobra.Command{Use: "list", Short: "List unresolved remote cleanup items", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		dir, err := workspaceDirectory(workspacePath())
		if err != nil {
			setExitCode(13)
			return err
		}
		ws, err := workspace.Open(dir)
		if err != nil {
			setExitCode(13)
			return fmt.Errorf("INFRA_ERROR: cannot open workspace")
		}
		defer ws.Close()
		lease, err := ws.TryLock()
		if err != nil {
			if errors.Is(err, workspace.ErrBusy) {
				setExitCode(16)
				return fmt.Errorf("WORKSPACE_BUSY")
			}
			setExitCode(13)
			return err
		}
		defer lease.Close()
		store, err := runstore.Open(ws)
		if err != nil {
			setExitCode(13)
			return err
		}
		type cleanupRow struct {
			RunID string             `json:"run_id"`
			Item  report.CleanupItem `json:"item"`
		}
		rows := []cleanupRow{}
		for _, run := range store.List() {
			for _, item := range store.CleanupItems(run.Acceptance.RunID) {
				if item.Status == "CLEAN" || item.Status == "IGNORED" {
					continue
				}
				item.OwnerToken = ""
				rows = append(rows, cleanupRow{RunID: run.Acceptance.RunID, Item: item})
			}
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
		}
		for _, row := range rows {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", row.RunID, row.Item.HostID, row.Item.Status); err != nil {
				return err
			}
		}
		return nil
	}}
	list.Flags().BoolVar(&jsonOutput, "json", false, "Output cleanup items as JSON")
	cleanup.AddCommand(list)
	return cleanup
}
