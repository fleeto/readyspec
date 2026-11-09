package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

func TestPrivilegedShellScriptVerifiesPayloadsWithOpenSSL(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("controlled shell fixture requires Linux")
	}
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("true executable unavailable")
	}
	binary, err := os.ReadFile(truePath)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	rootDir := filepath.Join(root, "privileged")
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "goss"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "privileged.yaml"), []byte("fixture config"), 0600); err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mkdir", "chmod", "cp", "rm", "env"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s executable unavailable", name)
		}
		if err := os.Symlink(path, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	chown := filepath.Join(tools, "chown")
	if err := os.WriteFile(chown, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	opensslPath, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl executable unavailable")
	}
	openssl := filepath.Join(tools, "openssl")
	if err := os.WriteFile(openssl, []byte("#!/bin/sh\nexec "+opensslPath+" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(readDirNames(t, tools), ","), "sha256sum") {
		t.Fatal("test PATH unexpectedly contains sha256sum")
	}

	artifact := compiler.Artifact{Config: []byte("fixture config")}
	script := privilegedShellScript(filepath.Join(stage, "goss"), filepath.Join(stage, "privileged.yaml"), rootDir, artifact, binary, "0123456789abcdef0123456789abcdef")
	command := exec.Command("/bin/sh", "-c", script)
	command.Env = []string{"PATH=" + tools}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("privileged shell did not verify through OpenSSL: err=%v output=%s", err, output)
	}
	if _, err := os.Stat(rootDir); !os.IsNotExist(err) {
		t.Fatalf("privileged workspace remained after successful execution: stat err=%v", err)
	}
}

func readDirNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result
}

func TestPrivilegedShellScriptPreservesExistingWorkspace(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "occupied")
	if err := os.Mkdir(rootDir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(rootDir, "existing-data")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	script := privilegedShellScript("/missing/goss", "/missing/config", rootDir, compiler.Artifact{}, nil, "0123456789abcdef0123456789abcdef")
	if output, err := exec.Command("/bin/sh", "-c", script).CombinedOutput(); err == nil {
		t.Fatalf("occupied workspace was accepted: %s", output)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "preserve" {
		t.Fatalf("existing workspace was modified: data=%q err=%v", data, err)
	}
}
