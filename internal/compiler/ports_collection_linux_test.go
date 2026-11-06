//go:build linux

package compiler_test

import (
	"fmt"
	"testing"
)

func ssFixture(v4, v6 string, rejectV4 bool) string {
	script := `case "$*" in
'-H -l -n -e -4 -t') cat /ss-v4 ;;
'-H -l -n -e -6 -t') cat /ss-v6 ;;
*) exit 99 ;;
esac`
	if rejectV4 {
		script = `case "$*" in
'-H -l -n -e -6 -t') cat /ss-v6 ;;
'-H -l -n -e -4 -t') exit 99 ;;
*) exit 99 ;;
esac`
	}
	return script
}

func TestPortCollectorValidatesScopesAndMetadata(t *testing.T) {
	for _, tt := range []struct {
		name, protocol, v4, v6, target, status string
		listening                              bool
		rejectV4                               bool
	}{
		{"ipv4_mapped_listener", "tcp", "", "LISTEN 0 128 [::ffff:127.0.0.1]:23234 [::]:*\n", "127.0.0.1", "FAIL", false, false},
		{"scoped_loopback", "tcp", "LISTEN 0 128 127.0.0.1%lo:23234 0.0.0.0:*\n", "", "127.0.0.1", "FAIL", false, false},
		{"users_metadata_ignored", "tcp", `LISTEN 0 128 127.0.0.1:23234 0.0.0.0:* users:(("sshd",pid=1,fd=3))` + "\n", "", "127.0.0.1", "FAIL", false, false},
		{"dual_stack_without_v6only", "tcp", "", "LISTEN 0 128 [::]:23234 [::]:* ino:1 sk:2\n", "127.0.0.1", "ERROR", false, false},
		{"dual_stack_v6only_0", "tcp", "", "LISTEN 0 128 [::]:23234 [::]:* v6only:0 ino:1 sk:2\n", "127.0.0.1", "FAIL", false, false},
		{"dual_stack_v6only_1", "tcp", "", "LISTEN 0 128 [::]:23234 [::]:* v6only:1 ino:1 sk:2\n", "127.0.0.1", "PASS", false, false},
		{"port_zero", "tcp", "LISTEN 0 128 127.0.0.1:0 0.0.0.0:*\n", "", "127.0.0.1", "ERROR", false, false},
		{"port_out_of_range", "tcp", "LISTEN 0 128 127.0.0.1:70000 0.0.0.0:*\n", "", "127.0.0.1", "ERROR", false, false},
		{"non_listen_state", "tcp", "ESTAB 0 0 127.0.0.1:23234 0.0.0.0:*\n", "", "127.0.0.1", "ERROR", false, false},
		{"truncated_record", "tcp", "LISTEN 0 128\n", "", "127.0.0.1", "ERROR", false, false},
		{"ipv6_only_protocol_skips_v4", "tcp6", "", "LISTEN 0 128 [::1]:23234 [::]:*\n", "::1", "FAIL", false, true},
		{"ipv6_only_protocol_free", "tcp6", "", "LISTEN 0 128 [::2]:23234 [::]:*\n", "::1", "PASS", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "ss-v4", tt.v4)
			fixtureFile(t, root, "ss-v6", tt.v6)
			fixtureTool(t, root, "tools/ss", ssFixture(tt.v4, tt.v6, tt.rejectV4))
			params := map[string]any{"protocol": tt.protocol, "port": float64(23234), "listening": tt.listening}
			if tt.target != "" {
				params["ip"] = []any{tt.target}
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "N02", params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
		})
	}
	t.Run("fixture_parameter_contract", func(t *testing.T) {
		root := collectionRoot(t)
		fixtureTool(t, root, "tools/ss", fmt.Sprintf("printf 'unexpected invocation: %%s\\n' \"$*\" >&2; exit 99"))
		results := collectInSandbox(t, root, collectionArtifact(t, "N02", map[string]any{"protocol": "tcp", "port": float64(23234), "listening": false}))
		if len(results) != 1 || results[0].Status != "ERROR" {
			t.Fatalf("unexpected ss invocation must not be trusted: %+v", results)
		}
	})
}
