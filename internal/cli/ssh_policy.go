package cli

import (
	"github.com/fleeto/readyspec/internal/executor"
	"github.com/spf13/cobra"
)

// Direct commands use their own process policy; delegated commands use the
// active service's policy rather than silently changing its configuration.
func nativeSSHForCommand(cmd *cobra.Command) (*executor.NativeSSH, error) {
	value := ""
	if flag := cmd.Flag("ssh-host-key-policy"); flag != nil {
		value = flag.Value.String()
	}
	policy, err := executor.ParseHostKeyPolicy(value)
	if err != nil {
		return nil, err
	}
	native := executor.NewNativeSSH()
	native.HostKeyPolicy = policy
	return native, nil
}
