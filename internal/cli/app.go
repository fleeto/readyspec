// Package cli exposes the Go control plane's implemented commands.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fleeto/readyspec/internal/buildinfo"
	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/goss"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/planstore"
	"github.com/fleeto/readyspec/internal/template"
	"github.com/fleeto/readyspec/internal/workspace"
	"github.com/spf13/cobra"
)

const Version = buildinfo.Version

func Execute(args []string, out, stderr io.Writer) int {
	if err := goss.Verify(); err != nil {
		fmt.Fprintln(stderr, "INFRA_ERROR:", err)
		return 13
	}
	root := &cobra.Command{
		Use: "readyspec", Short: "Pre-deployment environment checks and readiness gates",
		Version: Version, SilenceUsage: true, SilenceErrors: true,
	}
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(stderr)
	options := serveOptions{}
	root.PersistentFlags().StringVar(&options.listen, "listen", "127.0.0.1:18480", "Web console listener address")
	root.PersistentFlags().StringVar(&options.workspace, "workspace", "", "Workspace directory (READYSPEC_WORKSPACE takes precedence)")
	root.PersistentFlags().StringVar(&options.token, "auth-token", "", "Access token (prefer READYSPEC_AUTH_TOKEN)")
	root.PersistentFlags().StringVar(&options.cert, "tls-cert", "", "HTTPS certificate file")
	root.PersistentFlags().StringVar(&options.key, "tls-key", "", "HTTPS private key file")
	root.PersistentFlags().StringSliceVar(&options.allowedHosts, "allowed-host", nil, "Additional accepted Host values, including port")
	root.PersistentFlags().BoolVar(&options.noBrowser, "no-browser", false, "Do not launch the desktop browser")
	root.PersistentFlags().StringVar(&options.logLevel, "log-level", "", "Operational log level: debug, info, warn, or error (READYSPEC_LOG_LEVEL)")
	root.PersistentFlags().StringVar(&options.logFormat, "log-format", "", "Operational log format: text or json (READYSPEC_LOG_FORMAT)")
	root.PersistentFlags().StringVar(&options.logFile, "log-file", "", "Private rotating operational log file (READYSPEC_LOG_FILE)")
	exitCode := 0
	startServer := func(cmd *cobra.Command, args []string) error {
		err := serve(cmd.Context(), options, cmd.Flags().Changed("listen"), cmd.OutOrStdout())
		if err != nil {
			exitCode = 13
			if errors.Is(err, workspace.ErrBusy) {
				exitCode = 16
			}
			if errors.Is(err, errAuth) {
				exitCode = 15
			}
		}
		return err
	}
	root.Args = cobra.NoArgs
	root.RunE = startServer
	root.AddCommand(&cobra.Command{Use: "serve", Short: "Start the embedded Web console", Args: cobra.NoArgs, RunE: startServer})
	var asJSON bool
	validate := &cobra.Command{
		Use: "validate <file>", Short: "Validate a self-contained plan without network access",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return fmt.Errorf("PLAN_ERROR: cannot open plan file")
			}
			defer f.Close()
			p, err := plan.Load(f)
			if err != nil {
				return err
			}
			result := struct {
				PlanID  string `json:"plan_id"`
				Version string `json:"version"`
				State   string `json:"state"`
				Digest  string `json:"digest"`
			}{p.PlanID, p.Version, p.State, p.Digest}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "VALID %s@%s %s %s\n", p.PlanID, p.Version, p.State, p.Digest)
			return err
		},
	}
	validate.Flags().BoolVar(&asJSON, "json", false, "Output validation metadata as JSON")
	plans := &cobra.Command{Use: "plan", Short: "Manage inspection plans"}
	plans.AddCommand(validate)
	compile := &cobra.Command{Use: "compile <file>", Short: "Compile checks without connecting to target hosts", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		f, err := os.Open(args[0])
		if err != nil {
			return fmt.Errorf("PLAN_ERROR: cannot open plan file")
		}
		defer f.Close()
		p, err := plan.Load(f)
		if err != nil {
			return err
		}
		artifact, err := compiler.CompilePlan(p)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(artifact)
	}}
	plans.AddCommand(compile)
	plans.AddCommand(newPlanLifecycleCommands(func() string { return options.workspace }, func(code int) { exitCode = code })...)
	root.AddCommand(plans)
	root.AddCommand(newReportCommand(func() string { return options.workspace }, func(code int) { exitCode = code }))
	root.AddCommand(newCleanupCommand(func() string { return options.workspace }, func(code int) { exitCode = code }))
	templates := &cobra.Command{Use: "template", Short: "Inspect locally available check templates"}
	var templateJSON bool
	list := &cobra.Command{Use: "list", Short: "List implemented builtin check templates", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		entries := template.List()
		if data, err := localRunRequest(cmd.Context(), options.workspace, "GET", "/api/v1/templates", nil); err == nil {
			var response struct {
				Items []map[string]any `json:"items"`
			}
			if json.Unmarshal(data, &response) == nil {
				entries = response.Items
			}
		} else if isLocalServiceResponse(err) {
			return err
		} else if dir, dirErr := workspaceDirectory(options.workspace); dirErr == nil {
			if ws, openErr := workspace.Open(dir); openErr == nil {
				if lease, lockErr := ws.TryLock(); lockErr == nil {
					if plans, planErr := planstore.New(ws); planErr == nil {
						if public, listErr := plans.PublicTemplates(); listErr == nil {
							for _, item := range public {
								entries = append(entries, item.Snapshot)
							}
						} else {
							_ = lease.Close()
							_ = ws.Close()
							return listErr
						}
					}
					_ = lease.Close()
				}
				_ = ws.Close()
			}
		}
		if templateJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"items": entries})
		}
		for _, entry := range entries {
			fmt.Fprintf(cmd.OutOrStdout(), "%s@%s\t%s\t%s\n", entry["template_id"], entry["version"], entry["name"], entry["execution_mode"])
		}
		return nil
	}}
	list.Flags().BoolVar(&templateJSON, "json", false, "Output template snapshots as JSON")
	templates.AddCommand(list)
	root.AddCommand(templates)
	root.AddCommand(newRunCommand(func() string { return options.workspace }, func(code int) { exitCode = code }))
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		if exitCode == 0 {
			exitCode = 12
		}
		return exitCode
	}
	// Validation success is not a gate result: only future run submit can emit
	// READY. This command deliberately outputs VALID rather than a Verdict.
	return exitCode
}
