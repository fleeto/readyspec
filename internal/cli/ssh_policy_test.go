package cli_test

import (
	"bytes"
	"github.com/fleeto/readyspec/internal/cli"
	"strings"
	"testing"
)

func TestSSHHostKeyPolicyDefaultsOverridesAndRejectsInvalidConfiguration(t *testing.T) {
	for _, policy := range []string{"record-only", "strict"} {
		t.Run(policy, func(t *testing.T) {
			t.Setenv("READYSPEC_SSH_HOST_KEY_POLICY", "invalid-private-fixture")
			var out, stderr bytes.Buffer
			code := cli.Execute([]string{"--ssh-host-key-policy", policy, "plan", "validate", "../../examples/minimal-plan.yaml"}, &out, &stderr)
			if code != 0 {
				t.Fatalf("explicit policy did not override environment: %d %s", code, stderr.String())
			}
		})
	}
	t.Setenv("READYSPEC_SSH_HOST_KEY_POLICY", "invalid-private-fixture")
	var out, stderr bytes.Buffer
	code := cli.Execute([]string{"plan", "validate", "../../examples/minimal-plan.yaml"}, &out, &stderr)
	if code != 12 || !strings.Contains(stderr.String(), "SSH_HOST_KEY_POLICY_INVALID") || strings.Contains(stderr.String(), "invalid-private-fixture") {
		t.Fatalf("invalid policy not safely rejected: %d %s", code, stderr.String())
	}
	t.Setenv("READYSPEC_SSH_HOST_KEY_POLICY", "")
	out.Reset()
	stderr.Reset()
	if code := cli.Execute([]string{"--help"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), `default "record-only"`) {
		t.Fatalf("default policy not documented: %d %s", code, out.String())
	}
}
