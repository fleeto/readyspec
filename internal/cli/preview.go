package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/preview"
	"github.com/fleeto/readyspec/internal/safeyaml"
	"github.com/spf13/cobra"
)

func newRunCommand(workspacePath func() string, setExitCode func(int)) *cobra.Command {
	run := &cobra.Command{Use: "run", Short: "Preview inspection inputs"}
	var planFile, inventoryInput, format, overridesFile string
	var roleFlags []string
	var jsonOutput bool
	command := &cobra.Command{Use: "preview", Short: "Preview checks and compile host artifacts without SSH", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		file, err := os.Open(planFile)
		if err != nil {
			return fmt.Errorf("PLAN_ERROR: cannot open plan file")
		}
		defer file.Close()
		p, err := plan.Load(file)
		if err != nil {
			return err
		}
		source := inventoryInput
		inventoryFormat := format
		if info, err := os.Stat(inventoryInput); err == nil && !info.IsDir() {
			raw, err := readBoundedFile(inventoryInput)
			if err != nil {
				return err
			}
			source = string(raw)
			if inventoryFormat == "auto" {
				switch strings.ToLower(filepath.Ext(inventoryInput)) {
				case ".yaml", ".yml", ".json":
					inventoryFormat = "yaml"
				default:
					inventoryFormat = "ini"
				}
			}
		} else if format == "auto" {
			inventoryFormat = "quick"
		}
		hosts, err := inventory.Parse(source, inventoryFormat)
		if err != nil {
			return err
		}
		roleMap := map[string][]string{}
		for _, flag := range roleFlags {
			role, targets, ok := strings.Cut(flag, "=")
			if !ok || role == "" || targets == "" {
				return fmt.Errorf("PLAN_ERROR: expected --role-map role=host[,host]")
			}
			if _, exists := roleMap[role]; exists {
				return fmt.Errorf("PLAN_ERROR: duplicate role mapping")
			}
			roleMap[role] = strings.Split(targets, ",")
		}
		var overrides []preview.Override
		if overridesFile != "" {
			raw, err := readBoundedFile(overridesFile)
			if err != nil {
				return err
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&overrides); err != nil {
				return fmt.Errorf("PLAN_ERROR: invalid field override JSON")
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return fmt.Errorf("PLAN_ERROR: multiple override documents")
			}
		}
		result, err := preview.Build(p, preview.Input{Hosts: hosts, RoleMap: roleMap, Overrides: overrides})
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "STATIC PREVIEW: %d hosts, %d candidate checks, %d privileged, %d custom Shell, %d active probes\n", result.HostCount, result.CheckCount, result.PrivilegedCount, result.CustomShellCount, result.ActiveProbeCount)
		fmt.Fprintln(cmd.OutOrStdout(), "No target connections were made. Target identities, credentials and readiness are unverified.")
		if len(result.ConfiguredRootHosts) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Configured root logins: %s\n", strings.Join(result.ConfiguredRootHosts, ", "))
		}
		return nil
	}}
	command.Flags().StringVar(&planFile, "plan", "", "Self-contained plan file")
	command.Flags().StringVar(&inventoryInput, "inventory", "", "Inventory file or quick host list")
	command.Flags().StringVar(&format, "inventory-format", "auto", "Inventory format: auto, quick, ini, yaml")
	command.Flags().StringArrayVar(&roleFlags, "role-map", nil, "Role binding: role=host[,host] (repeat per role)")
	command.Flags().StringVar(&overridesFile, "field-overrides", "", "JSON file containing scoped field overrides")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Output static preview and host artifacts as JSON")
	_ = command.MarkFlagRequired("plan")
	_ = command.MarkFlagRequired("inventory")
	run.AddCommand(command)
	run.AddCommand(newSubmitCommand(workspacePath, setExitCode))
	run.AddCommand(newRunControlCommands(workspacePath, setExitCode)...)
	return run
}

func readBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("PLAN_ERROR: cannot open input file")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, safeyaml.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("PLAN_ERROR: cannot read input file")
	}
	if len(raw) > safeyaml.MaxBytes {
		return nil, fmt.Errorf("PLAN_ERROR: input exceeds size limit")
	}
	return raw, nil
}
