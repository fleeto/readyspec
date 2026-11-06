//go:build linux

package compiler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/goss"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/template"
)

// Re-executed only inside bubblewrap's isolated network namespace. The socket
// stays open while the original Goss artifact observes it via the real ss tool.
func TestSocketCollectionHelper(t *testing.T) {
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) != 3 {
		return
	}
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		if args[2] == "default" {
			return nil
		}
		var optionErr error
		err := c.Control(func(fd uintptr) {
			value := 0
			if args[2] == "v6only" {
				value = 1
			}
			optionErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, value)
		})
		if err != nil {
			return err
		}
		return optionErr
	}}
	var closer interface{ Close() error }
	var err error
	if args[0] == "udp" || args[0] == "udp6" {
		closer, err = lc.ListenPacket(context.Background(), args[0], args[1])
	} else {
		closer, err = lc.Listen(context.Background(), args[0], args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	out, err := exec.Command("/goss", "-g", "/goss.json", "validate", "--format", "json").Output()
	_ = closer.Close()
	_, _ = os.Stdout.Write(out)
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestBoundAddressPortAvailabilityCollectsRealSockets(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, network, address, option, target string
		listening                              bool
		status                                 string
	}{
		{"tcp_exact_busy", "tcp", "127.0.0.1:23234", "default", "127.0.0.1", false, "FAIL"},
		{"tcp_other_address_free", "tcp", "127.0.0.1:23234", "default", "127.0.0.2", false, "PASS"},
		{"tcp_wildcard_busy", "tcp", "0.0.0.0:23234", "default", "127.0.0.2", false, "FAIL"},
		{"tcp_exact_listening", "tcp", "127.0.0.1:23234", "default", "127.0.0.1", true, "PASS"},
		{"udp_exact_busy", "udp", "127.0.0.1:23234", "default", "127.0.0.1", false, "FAIL"},
		{"udp_other_address_free", "udp", "127.0.0.1:23234", "default", "127.0.0.2", false, "PASS"},
		{"udp_wildcard_busy", "udp", "0.0.0.0:23234", "default", "127.0.0.2", false, "FAIL"},
		{"udp_dual_stack", "udp6", "[::]:23234", "dual", "127.0.0.1", false, "FAIL"},
		{"global_port_busy", "tcp", "127.0.0.1:23234", "default", "", false, "FAIL"},
		{"global_port_listening", "tcp", "127.0.0.1:23234", "default", "", true, "PASS"},
		{"ipv6_wildcard_busy", "tcp6", "[::]:23234", "v6only", "::1", false, "FAIL"},
		{"dual_stack_conflict", "tcp6", "[::]:23234", "dual", "127.0.0.1", false, "FAIL"},
		{"ipv6_only_leaves_ipv4_free", "tcp6", "[::]:23234", "v6only", "127.0.0.1", false, "PASS"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			if err := os.WriteFile(filepath.Join(root, "fixture-helper"), helper, 0700); err != nil {
				t.Fatal(err)
			}
			protocol := "tcp"
			if strings.HasPrefix(tt.network, "udp") {
				protocol = "udp"
			}
			params := map[string]any{"protocol": protocol, "port": float64(23234), "listening": tt.listening}
			if tt.target != "" {
				params["ip"] = []any{tt.target}
			}
			a := collectionArtifact(t, "N02", params)
			if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := sandboxCommand(root, "/fixture-helper", "-test.run=^TestSocketCollectionHelper$", "--", tt.network, tt.address, tt.option)
			// iproute2 ships ss in /usr/sbin on RHEL-family distributions.
			for i, arg := range cmd.Args {
				if arg == "--" {
					args := append([]string{}, cmd.Args[:i]...)
					args = append(args, "--ro-bind", "/usr/sbin", "/usr/sbin", "--setenv", "PATH", "/tools:/bin:/usr/sbin")
					cmd.Args = append(args, cmd.Args[i:]...)
					break
				}
			}
			out, err := cmd.Output()
			if err != nil && len(out) == 0 {
				t.Fatalf("socket helper: %v", err)
			}
			results, err := compiler.Normalize(a, out)
			if err != nil || len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v err=%v output=%s", tt.status, results, err, out)
			}
		})
	}
}

// The original artifact runs in an isolated filesystem and network namespace.
// Only system binaries/libraries are shared read-only; /proc, /sys and /etc are
// fixture files. No developer services or configuration are inspected.
func collectionRoot(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		if os.Getenv("READYSPEC_REQUIRE_COLLECTION_SANDBOX") == "1" {
			t.Fatal("isolated collection requires bubblewrap")
		}
		t.Skip("isolated collection requires bubblewrap")
	}
	root := t.TempDir()
	for _, dir := range []string{"bin", "lib", "lib64", "dev", "tools", "usr/sbin", "usr/lib", "usr/lib64", "proc", "sys/module", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/bin", filepath.Join(root, "usr/bin")); err != nil {
		t.Fatal(err)
	}
	fixtureFile(t, root, "etc/passwd", fmt.Sprintf("fixture-user:x:%d:%d::/app:/bin/sh\n", os.Getuid(), os.Getgid()))
	fixtureFile(t, root, "etc/group", fmt.Sprintf("fixture-group:x:%d:\n", os.Getgid()))
	fixtureFile(t, root, "proc/self/mountinfo", "24 1 8:1 / /data rw - xfs /dev/fixture rw\n")
	// Resolve the installed awk alternative without sharing the host's /etc.
	awk, err := exec.LookPath("awk")
	if err != nil {
		t.Fatal(err)
	}
	awk, err = filepath.EvalSymlinks(awk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/"+filepath.Base(awk), filepath.Join(root, "tools/awk")); err != nil {
		t.Fatal(err)
	}
	payload, err := goss.Binary(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "goss"), payload, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func fixtureFile(t *testing.T, root, path, content string) {
	t.Helper()
	target := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func fixtureTool(t *testing.T, root, name, script string) {
	t.Helper()
	fixtureFile(t, root, name, "#!/bin/sh\nset -eu\n"+script+"\n")
	if err := os.Chmod(filepath.Join(root, name), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestOriginalCollectorsReadIsolatedSystemSources(t *testing.T) {
	for _, tt := range []struct {
		name, id     string
		params       map[string]any
		files, tools map[string]string
		status       string
	}{
		{"arch_alias", "H01", map[string]any{"allowed_arch": []any{"aarch64"}}, nil, map[string]string{"tools/uname": `[ "$*" = '-m' ]; printf arm64`}, "PASS"},
		{"arch_unknown", "H01", map[string]any{"allowed_arch": []any{"aarch64"}}, nil, map[string]string{"tools/uname": `[ "$*" = '-m' ]; printf mystery`}, "FAIL"},
		{"online_cpu", "H02", map[string]any{"min_cores": float64(4)}, nil, map[string]string{"tools/getconf": `[ "$*" = '_NPROCESSORS_ONLN' ]; printf 4`}, "PASS"},
		{"cpu_query_failure", "H02", map[string]any{"min_cores": float64(4)}, nil, map[string]string{"tools/getconf": `exit 2`}, "ERROR"},
		{"memtotal_units", "H03", map[string]any{"min_memory": "1 GiB"}, map[string]string{"proc/meminfo": "MemTotal: 1048576 kB\nMemAvailable: 1 kB\n"}, nil, "PASS"},
		{"memtotal_invalid_unit", "H03", map[string]any{"min_memory": "1 GiB"}, map[string]string{"proc/meminfo": "MemTotal: 1048576 MB\n"}, nil, "ERROR"},
		{"swap_multiple", "H04", map[string]any{"mode": "minimum", "min_swap": "3 MiB"}, map[string]string{"proc/swaps": "Filename Type Size Used Priority\n/swap1 file 1024 0 -2\n/swap2 file 2048 2048 -3\n"}, nil, "PASS"},
		{"swap_disabled", "H04", map[string]any{"mode": "disabled"}, map[string]string{"proc/swaps": "Filename Type Size Used Priority\n"}, nil, "PASS"},
		{"swap_missing", "H04", map[string]any{"mode": "disabled"}, nil, nil, "ERROR"},
		{"os_release", "O01", map[string]any{"allowed_ids": []any{"debian"}, "minimum_version": "12"}, map[string]string{"etc/os-release": "ID=debian\nVERSION_ID=\"12.1\"\n"}, nil, "PASS"},
		{"os_missing_version", "O01", map[string]any{"allowed_ids": []any{"debian"}}, map[string]string{"etc/os-release": "ID=debian\n"}, nil, "ERROR"},
		{"kernel_numeric", "O02", map[string]any{"minimum_version": "6.9"}, nil, map[string]string{"tools/uname": `[ "$*" = '-r' ]; printf 6.12.1-custom`}, "PASS"},
		{"module_normalization", "O04", map[string]any{"name": "ip-tables", "loaded": true}, map[string]string{"proc/modules": "ip_tables 32768 0 - Live 0x0000\n"}, nil, "PASS"},
		{"module_missing", "O04", map[string]any{"name": "ip_tables", "loaded": false}, nil, nil, "ERROR"},
		{"load1", "R03", map[string]any{"window": float64(1), "maximum": float64(1)}, map[string]string{"proc/loadavg": "1.00 2.00 3.00 1/100 1\n"}, nil, "PASS"},
		{"load5", "R03", map[string]any{"window": float64(5), "maximum": float64(1)}, map[string]string{"proc/loadavg": "1.00 2.00 3.00 1/100 1\n"}, nil, "FAIL"},
		{"load15", "R03", map[string]any{"window": float64(15), "maximum": float64(3)}, map[string]string{"proc/loadavg": "1.00 2.00 3.00 1/100 1\n"}, nil, "PASS"},
		{"pid_limit_hard", "R04", map[string]any{"pid": float64(42), "limit": "NOFILE", "bound": "hard", "minimum": float64(2048)}, map[string]string{"proc/42/limits": "Max open files 1024 4096 files\n"}, nil, "PASS"},
		{"pid_limit_soft", "R04", map[string]any{"pid": float64(42), "limit": "NOFILE", "bound": "soft", "minimum": float64(2048)}, map[string]string{"proc/42/limits": "Max open files 1024 4096 files\n"}, nil, "FAIL"},
		{"uptime_minimum", "L01", map[string]any{"minimum_seconds": float64(60)}, map[string]string{"proc/uptime": "59.99 123.0\n"}, nil, "FAIL"},
		{"uptime_maximum", "L01", map[string]any{"minimum_seconds": float64(60), "maximum_seconds": float64(120)}, map[string]string{"proc/uptime": "120.01 123.0\n"}, nil, "FAIL"},
		{"process_counts", "R02", map[string]any{"name": "worker", "minimum_count": float64(2), "maximum_count": float64(2)}, nil, map[string]string{"tools/ps": `[ "$*" = '-eo pid=,comm=' ]; printf '1 worker\n2 worker\n3 other\n'`}, "PASS"},
		{"process_query_failure", "R02", map[string]any{"name": "worker", "minimum_count": float64(0)}, nil, map[string]string{"tools/ps": `exit 2`}, "ERROR"},
		{"service_static", "R01", map[string]any{"name": "app.service", "enabled": false, "running": true}, nil, map[string]string{"tools/systemctl": `[ "$*" = 'show --no-pager --all -p LoadState -p ActiveState -p UnitFileState -- app.service' ]; printf 'LoadState=loaded\nActiveState=active\nUnitFileState=static\n'`}, "PASS"},
		{"service_indirect", "R01", map[string]any{"name": "app.service", "enabled": true, "running": false}, nil, map[string]string{"tools/systemctl": `printf 'LoadState=loaded\nActiveState=inactive\nUnitFileState=indirect\n'`}, "FAIL"},
		{"ssh_match_context", "A03", map[string]any{"key": "permitrootlogin", "expected": "no", "match_context": "user=app,host=node,addr=192.0.2.1"}, nil, map[string]string{"usr/sbin/sshd": `[ "$*" = '-T -C user=app,host=node,addr=192.0.2.1' ]; printf 'permitrootlogin no\n'`}, "PASS"},
		{"ssh_query_failure_no_config_fallback", "A03", map[string]any{"key": "permitrootlogin", "expected": "no"}, map[string]string{"etc/ssh/sshd_config": "PermitRootLogin no\n"}, map[string]string{"usr/sbin/sshd": `exit 2`}, "ERROR"},
		{"python_version", "D03", map[string]any{"runtime": "python", "minimum_version": "3.11"}, nil, map[string]string{"tools/python3": `[ "$*" = '--version' ]; printf 'Python 3.12.1\n'`}, "PASS"},
		{"node_version", "D03", map[string]any{"runtime": "node", "minimum_version": "20"}, nil, map[string]string{"tools/node": `[ "$*" = '--version' ]; printf 'v20.1.0\n'`}, "PASS"},
		{"java_version", "D03", map[string]any{"runtime": "java", "minimum_version": "8"}, nil, map[string]string{"tools/java": `[ "$*" = '-version' ]; printf 'openjdk version "1.8.0_422"\n' >&2`}, "PASS"},
		{"config_env", "D05", map[string]any{"path": "/config", "format": "env", "key": "KEY", "expected": "value"}, map[string]string{"config": "KEY=value\n"}, nil, "PASS"},
		{"config_ini", "D05", map[string]any{"path": "/config", "format": "ini", "key": "app.KEY", "expected": "value"}, map[string]string{"config": "[app]\nKEY=value\n"}, nil, "PASS"},
		{"config_json", "D05", map[string]any{"path": "/config", "format": "json", "key": "/app/key", "expected": "value"}, map[string]string{"config": "{\"app\":{\"key\":\"value\"}}"}, nil, "PASS"},
		{"config_yaml", "D05", map[string]any{"path": "/config", "format": "yaml", "key": "/app/key", "regex": "^val"}, map[string]string{"config": "app:\n  key: value\n"}, nil, "PASS"},
		{"config_json_rejects_yaml", "D05", map[string]any{"path": "/config", "format": "json", "key": "/app/key", "expected": "value"}, map[string]string{"config": "app:\n  key: value\n"}, nil, "ERROR"},
		{"config_duplicate_key", "D05", map[string]any{"path": "/config", "format": "env", "key": "KEY", "expected": "value"}, map[string]string{"config": "KEY=value\nKEY=other\n"}, nil, "ERROR"},
		{"config_missing_key", "D05", map[string]any{"path": "/config", "format": "env", "key": "KEY", "expected": "value"}, map[string]string{"config": "OTHER=value\n"}, nil, "FAIL"},
		{"disk_available_not_total", "S04", map[string]any{"path": "/data", "min_available": "1 MiB"}, nil, map[string]string{"tools/stat": fixtureStatScript("1024\n8192\n2048\n1024\n10\n5\nxfs")}, "PASS"},
		{"disk_boundary", "S04", map[string]any{"path": "/data", "min_available": "1 MiB"}, nil, map[string]string{"tools/stat": fixtureStatScript("1024\n8192\n2048\n1023\n10\n5\nxfs")}, "FAIL"},
		{"inode_count", "S05", map[string]any{"path": "/data", "min_available": float64(5)}, nil, map[string]string{"tools/stat": fixtureStatScript("1024\n8192\n2048\n1024\n10\n5\nxfs")}, "PASS"},
		{"inode_percentage", "S05", map[string]any{"path": "/data", "min_percent": float64(50)}, nil, map[string]string{"tools/stat": fixtureStatScript("1024\n8192\n2048\n1024\n10\n5\nxfs")}, "PASS"},
		{"inode_unknown_total", "S05", map[string]any{"path": "/data", "min_available": float64(0)}, nil, map[string]string{"tools/stat": fixtureStatScript("1024\n8192\n2048\n1024\n0\n0\nxfs")}, "ERROR"},
		{"interface_ipv6_multiple", "N01", map[string]any{"name": "eth0", "up": true, "addresses": []any{"2001:db8::/64", "192.0.2.0/24"}}, nil, map[string]string{"tools/ip": `[ "$*" = '-j address show dev eth0' ]; printf '[{"flags":["UP"],"addr_info":[{"local":"2001:db8::1","prefixlen":64},{"local":"192.0.2.1","prefixlen":24}]}]'`}, "PASS"},
		{"route_source", "N06", map[string]any{"target": "192.0.2.2", "source": "192.0.2.1", "interface": "eth0", "gateway": "192.0.2.254"}, nil, map[string]string{"tools/ip": `[ "$*" = '-j route get 192.0.2.2 from 192.0.2.1' ]; printf '[{"dev":"eth0","gateway":"192.0.2.254"}]'`}, "PASS"},
		{"time_chrony", "R05", map[string]any{"allowed_services": []any{"chronyd"}}, nil, map[string]string{"tools/timedatectl": `[ "$*" = 'show -p NTPSynchronized --value' ]; printf yes`, "tools/systemctl": `[ "$*" = 'is-active chronyd' ]; printf active`}, "PASS"},
		{"time_timesyncd", "R05", map[string]any{"allowed_services": []any{"systemd-timesyncd"}}, nil, map[string]string{"tools/timedatectl": `printf yes`, "tools/systemctl": `[ "$*" = 'is-active systemd-timesyncd' ]; printf active`}, "PASS"},
		{"time_ntpd", "R05", map[string]any{"allowed_services": []any{"ntpd"}}, nil, map[string]string{"tools/timedatectl": `printf yes`, "tools/systemctl": `[ "$*" = 'is-active ntpd' ]; printf active`}, "PASS"},
		{"time_query_failure", "R05", map[string]any{"allowed_services": []any{"chronyd"}}, nil, map[string]string{"tools/timedatectl": `exit 2`, "tools/systemctl": `printf active`}, "ERROR"},
		{"deb_epoch_release_comparison", "D01", map[string]any{"name": "app", "manager": "dpkg", "installed": true, "minimum_version": "1:2.9-1", "maximum_version": "1:2.10-2"}, nil, map[string]string{"tools/dpkg-query": `[ "$*" = '-W -f ${db:Status-Status}\t${Version} -- app' ]; printf 'installed\t1:2.10-1'`}, "PASS"},
		{"deb_version_mismatch", "D01", map[string]any{"name": "app", "manager": "dpkg", "installed": true, "minimum_version": "2:1.0-1"}, nil, map[string]string{"tools/dpkg-query": `printf 'installed\t1:999.0-1'`}, "FAIL"},
		{"deb_db_failure", "D01", map[string]any{"name": "app", "manager": "dpkg", "installed": false}, nil, map[string]string{"tools/dpkg-query": `printf 'dpkg-query: database is corrupt' >&2; exit 2`}, "ERROR"},
		{"rpm_query_and_bounds", "D01", map[string]any{"name": "app", "manager": "rpm", "installed": true, "minimum_version": "1:2.9-1", "maximum_version": "1:2.11-1"}, nil, map[string]string{"tools/rpm": `case "$*" in
 '-q --qf %{EPOCHNUM}:%{VERSION}-%{RELEASE} -- app') printf '1:2.10-1';;
 '--eval %{lua:print(rpm.vercmp("1:2.10-1","1:2.9-1"))}') printf 1;;
 '--eval %{lua:print(rpm.vercmp("1:2.10-1","1:2.11-1"))}') printf -- -1;;
 *) exit 2;; esac`}, "PASS"},
		{"rpm_db_failure", "D01", map[string]any{"name": "app", "manager": "rpm", "installed": false}, nil, map[string]string{"tools/rpm": `printf 'error: rpmdb: cannot open database'; exit 1`}, "ERROR"},
		{"tcp_dns_failure_is_not_expected_unreachability", "N03", map[string]any{"address": "missing.invalid:23234", "reachable": false, "timeout_ms": float64(50)}, map[string]string{"etc/resolv.conf": "nameserver 127.0.0.1\noptions attempts:1 timeout:1\n"}, nil, "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			for path, content := range tt.files {
				fixtureFile(t, root, path, content)
			}
			for name, script := range tt.tools {
				fixtureTool(t, root, name, script)
			}
			a := collectionArtifact(t, tt.id, tt.params)
			results := collectInSandbox(t, root, a)
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			for _, evidence := range results[0].Evidence {
				if evidence.Source == "" || evidence.StartTime == "" || evidence.EndTime == "" {
					t.Fatal("collection source and observation times must survive normalization")
				}
			}
		})
	}
}

func TestNativeAndFileCollectorsUseIsolatedFilesystem(t *testing.T) {
	for _, tt := range []struct {
		name, id string
		params   map[string]any
		files    map[string]string
		status   string
	}{
		{"sysctl_value", "O03", map[string]any{"name": "vm.swappiness", "value": "10"}, map[string]string{"proc/sys/vm/swappiness": "10\n"}, "PASS"},
		{"sysctl_missing", "O03", map[string]any{"name": "vm.swappiness", "value": "10"}, nil, "FAIL"},
		{"file_permissions", "S02", map[string]any{"path": "/data", "mode": "0644"}, map[string]string{"data": "content"}, "PASS"},
		{"file_missing", "S02", map[string]any{"path": "/missing", "mode": "0644"}, nil, "ERROR"},
		{"mount_options", "S03", map[string]any{"path": "/data", "filesystem": "ext4", "required_options": []any{"ro", "noexec"}}, map[string]string{"proc/self/mountinfo": "24 1 8:1 / /data ro,noexec - ext4 /dev/fixture ro\n"}, "PASS"},
		{"mount_forbidden_option", "S03", map[string]any{"path": "/data", "forbidden_options": []any{"noexec"}}, map[string]string{"proc/self/mountinfo": "24 1 8:1 / /data ro,noexec - ext4 /dev/fixture ro\n"}, "FAIL"},
		{"mount_not_independent", "S03", map[string]any{"path": "/data"}, map[string]string{"proc/self/mountinfo": "24 1 8:1 / / rw - ext4 /dev/fixture rw\n"}, "FAIL"},
		{"user_attributes", "A01", map[string]any{"name": "app", "exists": true, "uid": float64(1234), "gid": float64(1234), "shell": "/bin/sh"}, map[string]string{"etc/passwd": "app:x:1234:1234::/app:/bin/sh\n", "etc/group": "app:x:1234:\n"}, "PASS"},
		{"user_absent_expected", "A01", map[string]any{"name": "absent", "exists": false}, map[string]string{"etc/passwd": "app:x:1234:1234::/app:/bin/sh\n"}, "PASS"},
		{"group_primary_and_extra", "A02", map[string]any{"name": "app", "gid": float64(1234), "members": []any{"primary", "extra"}}, map[string]string{"etc/group": "app:x:1234:extra\n", "etc/passwd": "primary:x:1000:1234::/app:/bin/sh\nextra:x:1001:1001::/extra:/bin/sh\n"}, "PASS"},
		{"group_missing", "A02", map[string]any{"name": "absent", "members": []any{}}, map[string]string{"etc/group": "app:x:1234:extra\n", "etc/passwd": "primary:x:1000:1234::/app:/bin/sh\n"}, "FAIL"},
		{"elf_static_never_executed", "D04", map[string]any{"path": "/goss", "architecture": map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH], "required_libraries": []any{}}, nil, "PASS"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			for path, content := range tt.files {
				fixtureFile(t, root, path, content)
			}
			if tt.id == "S03" {
				if err := os.Mkdir(filepath.Join(root, "data"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			a := collectionArtifact(t, tt.id, tt.params)
			results := collectInSandbox(t, root, a)
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
		})
	}
}

func TestJournalCollectorsRunAsStandardAndCheckCoverage(t *testing.T) {
	for _, tt := range []struct {
		name, id, events, older, status string
		sinceBoot                       bool
	}{
		{"oom_covered_zero", "L02", "", `{"__REALTIME_TIMESTAMP":"5000000000","MESSAGE":"older"}`, "PASS", false},
		{"oom_gap_zero", "L02", "", "", "ERROR", false},
		{"oom_known_violation_despite_gap", "L02", `{"__REALTIME_TIMESTAMP":"7000000000","MESSAGE":"Killed process 42 (worker)"}`, "", "FAIL", false},
		{"service_failure", "L03", `{"__REALTIME_TIMESTAMP":"7000000000","MESSAGE":"Main process exited, status=1/FAILURE","INVOCATION_ID":"one"}` + "\n" + `{"__REALTIME_TIMESTAMP":"7000000001","MESSAGE":"Failed with result exit-code","INVOCATION_ID":"one"}`, `{"__REALTIME_TIMESTAMP":"5000000000","MESSAGE":"older"}`, "FAIL", false},
		{"service_clean", "L03", "", `{"__REALTIME_TIMESTAMP":"5000000000","MESSAGE":"older"}`, "PASS", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "proc/uptime", "9000.0 0\n")
			fixtureFile(t, root, "older", tt.older+"\n")
			fixtureFile(t, root, "events", tt.events+"\n")
			fixtureFile(t, root, "var/log/journal/machine/system.journal", "")
			fixtureTool(t, root, "tools/date", `[ "$*" = '+%s' ]; printf 10000`)
			journalFixtureTools(t, root, tt.id, 1000)
			fixtureFile(t, root, "first", "")
			fixtureFile(t, root, "audit-older", journalRecord(5000, 10, "older"))
			fixtureFile(t, root, "audit", journalRecord(7000, 11, "ordinary activity"))
			params := map[string]any{"lookback_seconds": float64(3600)}
			if tt.id == "L03" {
				params["unit"] = "app.service"
			}
			a := collectionArtifact(t, tt.id, params)
			if a.ExecutionMode != "STANDARD" {
				t.Fatal("SPEC default journal collector must support STANDARD")
			}
			results := collectInSandbox(t, root, a)
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
		})
	}
}

func TestPathAndQueryPermissionFailuresAreCollectionErrors(t *testing.T) {
	for _, tt := range []struct {
		name, id  string
		params    map[string]any
		path      string
		directory bool
	}{
		{"path_parent_denied", "S01", map[string]any{"path": "/blocked/file", "exists": true}, "blocked", true},
		{"file_parent_denied", "S02", map[string]any{"path": "/blocked/file", "mode": "0644"}, "blocked", true},
		{"sysctl_denied", "O03", map[string]any{"name": "vm.swappiness", "value": "10"}, "proc/sys/vm/swappiness", false},
		{"config_denied", "D05", map[string]any{"path": "/blocked/file", "format": "env", "key": "KEY", "expected": "value"}, "blocked", true},
		{"apparmor_profiles_denied", "A04", map[string]any{"framework": "apparmor", "mode": "enforcing"}, "sys/kernel/security/apparmor/profiles", false},
		{"journal_file_denied", "L02", map[string]any{"lookback_seconds": float64(3600)}, "var/log/journal/machine/system.journal", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			path := tt.path
			if tt.directory {
				path += "/file"
			}
			fixtureFile(t, root, path, "10\n")
			fixtureFile(t, root, "sys/module/apparmor/parameters/enabled", "Y\n")
			target := filepath.Join(root, tt.path)
			if err := os.Chmod(target, 0000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(target, 0755) })
			a := collectionArtifact(t, tt.id, tt.params)
			results := collectInSandbox(t, root, a)
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("want ERROR got %+v", results)
			}
		})
	}
}

func TestDanglingSymlinkCollectionPolicy(t *testing.T) {
	root := collectionRoot(t)
	if err := os.Symlink("/missing-target", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, follow := range []bool{false, true} {
		params := map[string]any{"path": "/link", "exists": true, "follow_symlinks": follow}
		if !follow {
			params["filetype"] = "symlink"
		}
		a := collectionArtifact(t, "S01", params)
		results := collectInSandbox(t, root, a)
		expected := "PASS"
		if follow {
			expected = "FAIL"
		}
		if len(results) != 1 || results[0].Status != expected {
			t.Fatalf("follow=%v want %s got %+v", follow, expected, results)
		}
	}
}

func TestSystemdLimitConfigurationCollectorUsesSpecifiedUnitAndBound(t *testing.T) {
	for _, bound := range []string{"soft", "hard"} {
		t.Run(bound, func(t *testing.T) {
			root := collectionRoot(t)
			property := "LimitNOFILE"
			if bound == "soft" {
				property += "Soft"
			}
			fixtureTool(t, root, "tools/systemctl", `[ "$*" = 'show --no-pager --value -p `+property+` -- app.service' ]; printf 4096`)
			p := catalogPlan(t, "R04")
			doc, _ := template.Lookup("R04", "1.0.0-config")
			raw, _ := json.Marshal(doc)
			var snapshot plan.CheckTemplate
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			p.EmbeddedTemplates = []plan.CheckTemplate{snapshot}
			item := &p.Roles[0].Items[0]
			item.TemplateVersion = snapshot.Version
			item.TemplateDigest = snapshot.Digest
			item.Params = map[string]any{"unit": "app.service", "limit": "NOFILE", "bound": bound, "minimum": float64(4096)}
			a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
			results := collectInSandbox(t, root, a)
			if len(results) != 1 || results[0].Status != "PASS" || results[0].EvidenceMode != "CONFIG" {
				t.Fatalf("unexpected configuration evidence %+v", results)
			}
		})
	}
}

func TestMissingCollectorToolsFailWithoutFallback(t *testing.T) {
	for _, tt := range []struct {
		id, tool string
		params   map[string]any
	}{
		{"N02", "ss", map[string]any{"protocol": "tcp", "port": float64(8080), "listening": false, "ip": []any{"127.0.0.1"}}},
		{"N06", "ip", map[string]any{"target": "192.0.2.2", "interface": "eth0"}},
		{"N03", "python3", map[string]any{"address": "127.0.0.1:8080", "reachable": false}},
		{"N04", "python3", map[string]any{"name": "fixture.test", "server": "127.0.0.1:5353", "record_type": "A", "addrs": []any{"127.0.0.1"}}},
		{"N05", "python3", map[string]any{"url": "http://127.0.0.1:8080", "status": float64(200), "body_contains": "healthy"}},
		{"S04", "stat", map[string]any{"path": "/data", "min_available": "0 B"}},
		{"S05", "stat", map[string]any{"path": "/data", "min_available": float64(0)}},
		{"O03", "stat", map[string]any{"name": "vm.swappiness", "minimum": 0}},
		{"O04", "cat", map[string]any{"name": "ip_tables", "loaded": false}},
		{"S01", "python3", map[string]any{"path": "/data", "exists": true}},
		{"D02", "python3", map[string]any{"command": "tool"}},
		{"R02", "ps", map[string]any{"name": "worker", "minimum_count": 0}},
		{"R03", "cat", map[string]any{"window": 1, "maximum": 100}},
		{"L01", "cat", map[string]any{"minimum_seconds": 0}},
		{"R04", "cat", map[string]any{"pid": 42, "limit": "NPROC", "minimum": 0}},
		{"R05", "timedatectl", map[string]any{"allowed_services": []any{"chronyd"}}},
		{"R05", "systemctl", map[string]any{"allowed_services": []any{"chronyd"}}},
		{"R01", "systemctl", map[string]any{"name": "app.service", "enabled": false, "running": false}},
	} {
		t.Run(tt.id, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureTool(t, root, "tools/"+tt.tool, "exit 127")
			a := collectionArtifact(t, tt.id, tt.params)
			results := collectInSandbox(t, root, a)
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("missing tool must not pass %+v", results)
			}
		})
	}
}

func TestMemoryAndSwapCollectionPreservesRequiredEvidence(t *testing.T) {
	for _, tt := range []struct {
		id, path, content string
		params            map[string]any
		field             string
	}{
		{"H03", "proc/meminfo", "MemTotal: 1048576 kB\nMemAvailable: 1 kB\n", map[string]any{"min_memory": "1 GiB"}, "mem_total_kib"},
		{"H04", "proc/swaps", "Filename Type Size Used Priority\n/swap file 0 0 -2\n", map[string]any{"mode": "enabled"}, "devices"},
	} {
		t.Run(tt.id, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, tt.path, tt.content)
			a := collectionArtifact(t, tt.id, tt.params)
			results := collectInSandbox(t, root, a)
			if len(results) != 1 || results[0].Status != "PASS" {
				t.Fatalf("want PASS got %+v", results)
			}
			found := false
			for _, evidence := range results[0].Evidence {
				if actual, ok := evidence.Actual.(map[string]any); ok {
					_, found = actual[tt.field]
					if found {
						break
					}
				}
			}
			if !found {
				t.Fatalf("required evidence %s lost: %+v", tt.field, results)
			}
		})
	}
}

func sandboxCommand(root string, command ...string) *exec.Cmd {
	args := []string{"--die-with-parent", "--unshare-all", "--ro-bind", root, "/", "--ro-bind", "/usr/bin", "/bin", "--ro-bind", "/usr/lib", "/usr/lib"}
	// Distributions such as RHEL/AlmaLinux keep the Python standard library and
	// architecture-specific runtimes under /usr/lib64 rather than /usr/lib.
	if _, err := os.Stat("/usr/lib64"); err == nil {
		args = append(args, "--ro-bind", "/usr/lib64", "/usr/lib64")
	}
	args = append(args, "--ro-bind", "/lib", "/lib", "--ro-bind", "/lib64", "/lib64", "--dev", "/dev", "--clearenv", "--setenv", "PATH", "/tools:/bin", "--setenv", "LC_ALL", "C", "--")
	return exec.Command("bwrap", append(args, command...)...)
}

func collectionArtifact(t *testing.T, id string, params map[string]any) compiler.Artifact {
	t.Helper()
	p := catalogPlan(t, id)
	p.Roles[0].Items[0].Params = params
	return selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
}

func collectInSandbox(t *testing.T, root string, a compiler.Artifact) []compiler.ItemResult {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := sandboxCommand(root, "/goss", "-g", "/goss.json", "validate", "--format", "json").Output()
	if err != nil && len(out) == 0 {
		t.Fatalf("sandbox collection failed: %v", err)
	}
	results, err := compiler.Normalize(a, out)
	if err != nil {
		t.Fatalf("normalize: %v output=%s", err, out)
	}
	return results
}

func TestAppArmorModesCollectKernelProfiles(t *testing.T) {
	for _, tt := range []struct{ name, enabled, profiles, mode, status string }{
		{"enforce", "Y\n", "/usr/bin/app (enforce)\n", "enforcing", "PASS"},
		{"complain", "Y\n", "/usr/bin/app (complain)\n", "permissive", "PASS"},
		{"mode_mismatch", "Y\n", "/usr/bin/app (complain)\n", "enforcing", "FAIL"},
		{"mixed_not_enforcing", "Y\n", "app (enforce)\nother (complain)\n", "enforcing", "FAIL"},
		{"enabled_without_profiles", "Y\n", "", "enabled", "PASS"},
		{"mode_unknown_without_profiles", "Y\n", "", "enforcing", "ERROR"},
		{"disabled", "N\n", "", "disabled", "PASS"},
		{"disabled_not_enforcing", "N\n", "", "enforcing", "FAIL"},
		{"malformed", "Y\n", "unexpected output\n", "enforcing", "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "sys/module/apparmor/parameters/enabled", tt.enabled)
			fixtureFile(t, root, "sys/kernel/security/apparmor/profiles", tt.profiles)
			a := collectionArtifact(t, "A04", map[string]any{"framework": "apparmor", "mode": tt.mode})
			results := collectInSandbox(t, root, a)
			if len(results) != 1 || results[0].Status != tt.status {
				raw, _ := json.Marshal(results)
				t.Fatalf("want %s got %s", tt.status, raw)
			}
		})
	}
}
