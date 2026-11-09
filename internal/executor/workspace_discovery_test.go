package executor

import (
	"testing"
)

func TestSelectWorkspaceBaseSkipsNoexecAndReadOnlyMounts(t *testing.T) {
	directories := map[string]remoteDirectory{
		"/tmp":     {Exists: true, Writable: true, Executable: true},
		"/var/tmp": {Exists: true, Writable: true, Executable: true},
	}
	mounts := []string{
		"36 25 0:32 / / rw,nosuid - ext4 /dev/root rw",
		"45 36 0:47 / /tmp rw,nosuid,nodev,noexec - tmpfs tmpfs rw",
		"46 36 0:48 / /var/tmp rw,nosuid,nodev - tmpfs tmpfs rw",
	}
	selected, err := selectWorkspaceBase([]string{"/tmp", "/var/tmp"}, directories, mounts, true)
	if err != nil || selected != "/var/tmp" {
		t.Fatalf("selected workspace=%q, err=%v; want /var/tmp", selected, err)
	}
}

func TestSelectWorkspaceBaseUsesPrivilegedMountOrderWithoutUserWriteBit(t *testing.T) {
	directories := map[string]remoteDirectory{
		"/run":     {Exists: true, Executable: true},
		"/var/tmp": {Exists: true, Writable: true, Executable: true},
	}
	mounts := []string{
		"36 25 0:32 / / rw,nosuid - ext4 /dev/root rw",
		"40 36 0:40 / /run rw,nosuid,nodev - tmpfs tmpfs rw",
		"41 36 0:41 / /var/tmp rw,nosuid,nodev - tmpfs tmpfs rw",
	}
	selected, err := selectWorkspaceBase([]string{"/run", "/var/tmp"}, directories, mounts, false)
	if err != nil || selected != "/run" {
		t.Fatalf("selected privileged workspace=%q, err=%v; want /run", selected, err)
	}
}

func TestSelectWorkspaceBaseUsesVisibleOvermountedMountRecord(t *testing.T) {
	directories := map[string]remoteDirectory{
		"/run":     {Exists: true, Executable: true, MountID: "55"},
		"/var/tmp": {Exists: true, Writable: true, Executable: true, MountID: "41"},
	}
	mounts := []string{
		"36 25 0:32 / / rw,nosuid - ext4 /dev/root rw",
		"40 36 0:40 / /run rw,nosuid,nodev - tmpfs tmpfs rw",
		"41 36 0:41 / /var/tmp rw,nosuid,nodev - tmpfs tmpfs rw",
		"55 40 0:55 / /run rw,nosuid,nodev,noexec - tmpfs tmpfs rw",
	}
	selected, err := selectWorkspaceBase([]string{"/run", "/var/tmp"}, directories, mounts, false)
	if err != nil || selected != "/var/tmp" {
		t.Fatalf("selected privileged workspace=%q, err=%v; want /var/tmp", selected, err)
	}
}

func TestMountOptionsForVisibleParentMount(t *testing.T) {
	mounts := []string{
		"36 25 0:32 / / rw,nosuid - ext4 /dev/root rw",
		"45 36 0:47 / /tmp rw,nosuid,nodev,noexec - tmpfs tmpfs rw",
	}
	options, ok := mountOptionsFor("/var/tmp", mounts, "36")
	if !ok || hasMountOption(options, "noexec") {
		t.Fatalf("inherited root mount options=%q, found=%t; want executable root mount", options, ok)
	}
	options, ok = mountOptionsFor("/tmp", mounts, "45")
	if !ok || !hasMountOption(options, "noexec") {
		t.Fatalf("visible /tmp mount options=%q, found=%t; want noexec", options, ok)
	}
}

func TestSelectWorkspaceBaseFailsWhenCandidatesAreUnsafe(t *testing.T) {
	directories := map[string]remoteDirectory{
		"/tmp":           {Exists: true, Writable: true, Executable: true},
		"/var/tmp":       {Exists: true, Writable: true, Executable: true, Symlink: true},
		"/home/u/.cache": {Exists: true, Writable: true, Executable: true},
	}
	mounts := []string{
		"36 25 0:32 / / rw,nosuid - ext4 /dev/root rw",
		"45 36 0:47 / /tmp rw,nosuid,nodev,noexec - tmpfs tmpfs rw",
		"46 36 0:48 / /var/tmp ro,nosuid,nodev - tmpfs tmpfs ro",
		"47 36 0:49 / /home/u rw,nosuid,nodev,noexec - tmpfs tmpfs rw",
	}
	if selected, err := selectWorkspaceBase([]string{"/tmp", "/var/tmp", "/home/u/.cache"}, directories, mounts, true); err == nil {
		t.Fatalf("selected unsafe fallback %q", selected)
	}
}
