//go:build linux

package compiler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/compiler"
)

// Standalone syscalls keep the fixture library free of incidental libc
// dependencies. The executable's interpreter is a real ELF execution sentinel.
const elfFixtureSyscalls = `
#if defined(__x86_64__)
static long call4(long n,long a,long b,long c,long d) {
  register long r10 __asm__("r10")=d;
  long result;
  __asm__ volatile("syscall" : "=a"(result) : "a"(n),"D"(a),"S"(b),"d"(c),"r"(r10) : "rcx","r11","memory");
  return result;
}
#define OPENAT 257
#define WRITE 1
#define CLOSE 3
#define EXIT 60
#elif defined(__aarch64__)
static long call4(long n,long a,long b,long c,long d) {
  register long x8 __asm__("x8")=n;
  register long x0 __asm__("x0")=a;
  register long x1 __asm__("x1")=b;
  register long x2 __asm__("x2")=c;
  register long x3 __asm__("x3")=d;
  __asm__ volatile("svc #0" : "+r"(x0) : "r"(x8),"r"(x1),"r"(x2),"r"(x3) : "memory","cc");
  return x0;
}
#define OPENAT 56
#define WRITE 64
#define CLOSE 57
#define EXIT 93
#endif
static void mark(const char *path) {
  long fd=call4(OPENAT,-100,(long)path,577,0600);
  if (fd>=0) { call4(WRITE,fd,(long)"X",1,0); call4(CLOSE,fd,0,0,0); }
}
`

type elfFixture struct{ target, library, interpreterLibrary, loader, ldconfig, otherABI []byte }

func buildDynamicELFFixture(t *testing.T) elfFixture {
	t.Helper()
	dir := t.TempDir()
	build := func(name, source string, flags ...string) []byte {
		path := filepath.Join(dir, name)
		input := path + ".c"
		if err := os.WriteFile(input, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"-Wall", "-Werror", "-fno-stack-protector", "-o", path, input}, flags...)
		if out, err := exec.Command("cc", args...).CombinedOutput(); err != nil {
			t.Fatalf("build ELF fixture: %v %s", err, out)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	library := build("libreadyspec_fixture.so.1", elfFixtureSyscalls+`__attribute__((constructor)) static void guard(void) {mark("/state/library-executed");} int fixture_value(void) {return 7;}`,
		"-shared", "-fPIC", "-nostdlib", "-Wl,-soname,libreadyspec_fixture.so.1")
	loader := build("sentinel-loader", elfFixtureSyscalls+`void _start(void) {mark("/state/target-executed"); call4(EXIT,99,0,0,0); for (;;) {} }`, "-nostdlib", "-static", "-no-pie", "-Wl,-e,_start")
	target := build("probe", elfFixtureSyscalls+`extern int fixture_value(void); void _start(void) {mark("/state/target-executed"); volatile int result=fixture_value(); (void)result; for (;;) {} }`,
		"-nostdlib", "-fPIE", "-pie", "-Wl,-e,_start", "-L"+dir, "-Wl,-rpath,/opt/lib", "-Wl,--no-as-needed", "-l:libreadyspec_fixture.so.1")
	tool, err := exec.LookPath("ldconfig")
	if err != nil {
		t.Fatal(err)
	}
	ldconfig, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	// Ubuntu's ldconfig entry point can be a wrapper around ldconfig.real.
	// Copy the ELF executable so the isolated fixture has no host script paths.
	if len(ldconfig) >= 2 && string(ldconfig[:2]) == "#!" {
		ldconfig, err = os.ReadFile(filepath.Join(filepath.Dir(tool), "ldconfig.real"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(ldconfig) < 4 || string(ldconfig[:4]) != "\x7fELF" {
		t.Fatal("isolated ldconfig fixture requires an ELF executable")
	}
	fixture := elfFixture{target: target, library: library, loader: loader, ldconfig: ldconfig}
	fixture.interpreterLibrary = build("libinterp.so", elfFixtureSyscalls+`const char interp[] __attribute__((section(".interp")))="/sentinel-loader"; __attribute__((constructor)) static void guard(void) {mark("/state/library-executed");} int fixture_value(void) {return 7;}`,
		"-shared", "-fPIC", "-nostdlib", "-Wl,-soname,libreadyspec_fixture.so.1")
	if runtime.GOARCH == "amd64" {
		fixture.otherABI = build("lib32.so", `int fixture_value(void) {return 7;}`, "-m32", "-shared", "-fPIC", "-nostdlib", "-Wl,-soname,libreadyspec_fixture.so.1")
	}
	return fixture
}

func insertSandboxOptions(cmd *exec.Cmd, options ...string) *exec.Cmd {
	for i, arg := range cmd.Args {
		if arg == "--" {
			args := append([]string{}, cmd.Args[:i]...)
			args = append(args, options...)
			cmd.Args = append(args, cmd.Args[i:]...)
			return cmd
		}
	}
	panic("sandbox argument separator missing")
}

func prepareELFFixture(t *testing.T, fixture elfFixture, cacheLibrary []byte) string {
	t.Helper()
	root := collectionRoot(t)
	for _, path := range []string{"state", "opt/lib", "cache-root/etc", "cache-root/lib", "cache-root/usr/lib", "cache-root/opt/lib"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string, raw []byte) {
		if err := os.WriteFile(filepath.Join(root, path), raw, 0755); err != nil {
			t.Fatal(err)
		}
	}
	write("opt/probe", fixture.target)
	write("opt/lib/libreadyspec_fixture.so.1", fixture.library)
	write("sentinel-loader", fixture.loader)
	write("tools/ldconfig-real", fixture.ldconfig)
	if cacheLibrary != nil {
		write("cache-root/opt/lib/libreadyspec_fixture.so.1", cacheLibrary)
	}
	fixtureFile(t, root, "cache-root/etc/ld.so.conf", "/opt/lib\n")
	// Generate a real cache in a private chroot. Its only libraries are fixtures;
	// neither the host cache nor host library inventory is read or modified.
	cmd := sandboxCommand(root, "/tools/ldconfig-real", "-r", "/cache-root", "-X", "-i")
	insertSandboxOptions(cmd, "--bind", filepath.Join(root, "cache-root"), "/cache-root", "--uid", "0", "--gid", "0", "--cap-add", "CAP_SYS_CHROOT")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate isolated ld.so.cache: %v %s", err, out)
	}
	cache, err := os.ReadFile(filepath.Join(root, "cache-root/etc/ld.so.cache"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/ld.so.cache"), cache, 0644); err != nil {
		t.Fatal(err)
	}
	fixtureTool(t, root, "tools/ldconfig", `[ "$*" = '-p' ]; exec /tools/ldconfig-real -p -C /etc/ld.so.cache`)
	fixtureTool(t, root, "tools/ldd", `printf called > /state/ldd-called; exit 99`)
	return root
}

func elfSandboxCommand(root string, command ...string) *exec.Cmd {
	cmd := sandboxCommand(root, command...)
	insertSandboxOptions(cmd, "--bind", filepath.Join(root, "state"), "/state")
	// Guard both PATH lookup and the usual absolute ldd path.
	if _, err := os.Stat("/usr/bin/ldd"); err == nil {
		insertSandboxOptions(cmd, "--ro-bind", filepath.Join(root, "tools/ldd"), "/bin/ldd")
	}
	return cmd
}

func assertELFSentinelsUntouched(t *testing.T, root string) {
	t.Helper()
	for _, marker := range []string{"target-executed", "library-executed", "ldd-called"} {
		if _, err := os.Stat(filepath.Join(root, "state", marker)); !os.IsNotExist(err) {
			t.Fatalf("forbidden execution sentinel %s triggered or unreadable: %v", marker, err)
		}
	}
}

func TestDynamicELFCollectorUsesActualLibrariesAndCache(t *testing.T) {
	fixture := buildDynamicELFFixture(t)
	cases := []struct{ name, status string }{
		{"shared_library_with_interpreter", "PASS"},
		{"cache_hit", "PASS"}, {"wrong_architecture", "FAIL"}, {"required_not_needed", "FAIL"}, {"cache_miss", "FAIL"},
		{"stale_cache_path", "FAIL"}, {"invalid_library", "FAIL"}, {"library_unreadable", "ERROR"}, {"readelf_failure", "ERROR"}, {"cache_tool_failure", "ERROR"},
		{"truncated_library", "FAIL"}, {"pie_executable_as_library", "FAIL"},
		{"malformed_cache", "ERROR"}, {"incomplete_cache", "ERROR"}, {"missing_readelf", "ERROR"}, {"missing_cache_tool", "ERROR"},
	}
	if fixture.otherABI != nil {
		cases = append(cases, struct{ name, status string }{"cache_wrong_abi", "FAIL"})
		cases = append(cases, struct{ name, status string }{"library_wrong_abi", "FAIL"})
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cacheLib := fixture.library
			if tt.name == "cache_miss" {
				cacheLib = nil
			}
			if tt.name == "cache_wrong_abi" {
				cacheLib = fixture.otherABI
			}
			root := prepareELFFixture(t, fixture, cacheLib)
			if tt.name == "shared_library_with_interpreter" {
				if err := os.WriteFile(filepath.Join(root, "opt/lib/libreadyspec_fixture.so.1"), fixture.interpreterLibrary, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if tt.name == "library_wrong_abi" {
				if err := os.WriteFile(filepath.Join(root, "opt/lib/libreadyspec_fixture.so.1"), fixture.otherABI, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if tt.name == "malformed_cache" {
				fixtureTool(t, root, "tools/ldconfig", `printf 'invalid cache output\n'`)
			}
			if tt.name == "incomplete_cache" {
				fixtureTool(t, root, "tools/ldconfig", `printf '1 libs found in cache /etc/ld.so.cache\n'`)
			}
			if tt.name == "missing_readelf" {
				fixtureTool(t, root, "tools/readelf", "exit 127")
			}
			if tt.name == "missing_cache_tool" {
				fixtureTool(t, root, "tools/ldconfig", "exit 127")
			}
			if tt.name == "stale_cache_path" {
				if err := os.Remove(filepath.Join(root, "opt/lib/libreadyspec_fixture.so.1")); err != nil {
					t.Fatal(err)
				}
			}
			if tt.name == "invalid_library" {
				fixtureFile(t, root, "opt/lib/libreadyspec_fixture.so.1", "not an ELF library")
			}
			if tt.name == "truncated_library" {
				if err := os.WriteFile(filepath.Join(root, "opt/lib/libreadyspec_fixture.so.1"), fixture.library[:64], 0644); err != nil {
					t.Fatal(err)
				}
			}
			if tt.name == "pie_executable_as_library" {
				if err := os.WriteFile(filepath.Join(root, "opt/lib/libreadyspec_fixture.so.1"), fixture.target, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if tt.name == "library_unreadable" {
				if err := os.Chmod(filepath.Join(root, "opt/lib/libreadyspec_fixture.so.1"), 0000); err != nil {
					t.Fatal(err)
				}
			}
			if tt.name == "readelf_failure" {
				fixtureTool(t, root, "tools/readelf", "exit 2")
			}
			if tt.name == "cache_tool_failure" {
				fixtureTool(t, root, "tools/ldconfig", "exit 2")
			}
			arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
			if tt.name == "wrong_architecture" {
				if arch == "x86_64" {
					arch = "aarch64"
				} else {
					arch = "x86_64"
				}
			}
			required := []any{"libreadyspec_fixture.so.1"}
			if tt.name == "required_not_needed" {
				required = []any{"libnotneeded.so.1"}
			}
			a := collectionArtifact(t, "D04", map[string]any{"path": "/opt/probe", "architecture": arch, "required_libraries": required})
			if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
				t.Fatal(err)
			}
			out, err := elfSandboxCommand(root, "/goss", "-g", "/goss.json", "validate", "--format", "json").Output()
			if err != nil && len(out) == 0 {
				t.Fatal(err)
			}
			assertELFSentinelsUntouched(t, root)
			results, err := compiler.Normalize(a, out)
			if err != nil || len(results) != 1 || results[0].Status != tt.status {
				raw, _ := json.Marshal(results)
				t.Fatalf("want %s got %s error=%v", tt.status, raw, err)
			}
			codes := map[string]string{"readelf_failure": "READELF_FAILED", "missing_readelf": "READELF_FAILED", "cache_tool_failure": "CACHE_QUERY_FAILED", "missing_cache_tool": "CACHE_QUERY_FAILED", "malformed_cache": "INVALID_CACHE_OUTPUT", "incomplete_cache": "INCOMPLETE_CACHE_OUTPUT"}
			if code, exists := codes[tt.name]; exists {
				if got := evidenceProperty(t, results, "error"); got != code {
					t.Fatalf("want error %s got %v", code, got)
				}
				return
			}
			if got := fmt.Sprint(evidenceProperty(t, results, "needed")); got != "[libreadyspec_fixture.so.1]" {
				t.Fatalf("actual NEEDED list lost: %s", got)
			}
			if evidenceProperty(t, results, "cache_queried") != true {
				t.Fatal("dynamic dependency cache was not queried")
			}
			// Normalize the public evidence to its JSON shape for portable assertions.
			raw, _ := json.Marshal(evidenceProperty(t, results, "cache_entries"))
			var entries []map[string]any
			if err := json.Unmarshal(raw, &entries); err != nil {
				t.Fatal(err)
			}
			if tt.name == "cache_miss" {
				if len(entries) != 0 {
					t.Fatal("empty private cache unexpectedly resolved library")
				}
				return
			}
			if len(entries) != 1 || entries[0]["name"] != "libreadyspec_fixture.so.1" || entries[0]["path"] != "/opt/lib/libreadyspec_fixture.so.1" || entries[0]["flags"] == "" {
				t.Fatalf("cache candidate evidence incomplete: %v", entries)
			}
			statuses := map[string]string{"stale_cache_path": "MISSING", "invalid_library": "INVALID_ELF", "truncated_library": "INVALID_ELF", "library_unreadable": "UNREADABLE"}
			want := "OK"
			if status, ok := statuses[tt.name]; ok {
				want = status
			}
			if entries[0]["status"] != want {
				t.Fatalf("candidate want %s got %v", want, entries)
			}
			if tt.name == "cache_hit" {
				header, ok := entries[0]["header"].(map[string]any)
				if !ok || header["type"] != float64(3) || header["pie"] != false || header["class"] != float64(64) {
					t.Fatalf("compatible shared ELF evidence missing: %v", entries)
				}
			}
		})
	}
}

func TestELFCollectorExecutionSentinelsAreActive(t *testing.T) {
	fixture := buildDynamicELFFixture(t)
	for _, tt := range []struct {
		name, marker string
		command      []string
	}{
		{"target", "target-executed", []string{"/opt/probe"}},
		{"library", "library-executed", []string{"/bin/python3", "-c", `import ctypes; ctypes.CDLL('/opt/lib/libreadyspec_fixture.so.1')`}},
		{"ldd", "ldd-called", []string{"/bin/ldd", "/opt/probe"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := prepareELFFixture(t, fixture, fixture.library)
			cmd := elfSandboxCommand(root, tt.command...)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			run := exec.CommandContext(ctx, "timeout", "2")
			run.Args = append(run.Args, cmd.Args...)
			out, runErr := run.CombinedOutput()
			if _, err := os.Stat(filepath.Join(root, "state", tt.marker)); err != nil {
				t.Fatalf("execution sentinel does not detect %s: %v (invocation=%v output=%s)", tt.name, err, runErr, out)
			}
		})
	}
}
