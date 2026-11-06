//go:build linux && amd64

package workspace

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	journalSyscallCrashRootEnv = "READYSPEC_JOURNAL_SYSCALL_CRASH_ROOT"
	journalSyscallCrashFDEnv   = "READYSPEC_JOURNAL_SYSCALL_CRASH_FD"
)

type seccompNotification struct {
	ID    uint64
	PID   uint32
	Flags uint32
	Data  struct {
		Number int32
		Arch   uint32
		IP     uint64
		Args   [6]uint64
	}
}

// TestJournalHardKillDuringFsyncHelper is the child side of the seccomp test.
// The kernel holds its fsync syscall until the parent kills this process.
func TestJournalHardKillDuringFsyncHelper(t *testing.T) {
	root := os.Getenv(journalSyscallCrashRootEnv)
	if root == "" {
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	_ = lease
	if _, _, err := store.RecoverJSONLines("runs/demo/events.ndjson"); err != nil {
		t.Fatal(err)
	}

	listenerFD, err := installFsyncNotificationFilter()
	if err != nil {
		t.Fatalf("install seccomp fsync notification filter: %v", err)
	}
	defer unix.Close(listenerFD)
	controlFD, err := strconv.Atoi(os.Getenv(journalSyscallCrashFDEnv))
	if err != nil {
		t.Fatalf("parse notification control fd: %v", err)
	}
	if err := unix.Sendmsg(controlFD, []byte{1}, unix.UnixRights(listenerFD), nil, 0); err != nil {
		t.Fatalf("send seccomp notification fd: %v", err)
	}
	if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 2}); err != nil {
		t.Fatalf("append while fsync filter is installed: %v", err)
	}
	t.Fatal("filtered fsync returned without a notification response")
}

// TestJournalHardKillWhileFsyncSyscallIsBlocked sends SIGKILL only after the
// kernel has reported that the journal append entered fsync. It then verifies
// that reopening and recovering the journal can safely reconcile the record
// written immediately before that unsynchronized fsync.
func TestJournalHardKillWhileFsyncSyscallIsBlocked(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 1}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	sockets, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	parentSocket := os.NewFile(uintptr(sockets[0]), "seccomp-parent")
	childSocket := os.NewFile(uintptr(sockets[1]), "seccomp-child")
	cmd := exec.Command(os.Args[0], "-test.run=^TestJournalHardKillDuringFsyncHelper$")
	cmd.ExtraFiles = []*os.File{childSocket}
	cmd.Env = append(os.Environ(), journalSyscallCrashRootEnv+"="+root, journalSyscallCrashFDEnv+"=3")
	if err := cmd.Start(); err != nil {
		_ = parentSocket.Close()
		_ = childSocket.Close()
		t.Fatal(err)
	}
	_ = childSocket.Close()
	parentConnRaw, err := net.FileConn(parentSocket)
	_ = parentSocket.Close()
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("open parent notification channel: %v", err)
	}
	parentConn, ok := parentConnRaw.(*net.UnixConn)
	if !ok {
		_ = parentConnRaw.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("notification channel type is %T, want *net.UnixConn", parentConnRaw)
	}
	defer parentConn.Close()

	control := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(4))
	_, oobn, _, _, err := parentConn.ReadMsgUnix(control, oob)
	if err != nil {
		_ = cmd.Process.Kill()
		output, _ := cmd.CombinedOutput()
		t.Fatalf("receive seccomp notification fd: %v; child output: %s", err, output)
	} else if control[0] != 1 || oobn == 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("invalid seccomp fd handshake: payload=%v oob bytes=%d", control, oobn)
	}
	messages, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(messages) != 1 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("parse seccomp fd control message: messages=%d err=%v", len(messages), err)
	}
	listenerFDs, err := unix.ParseUnixRights(&messages[0])
	if err != nil || len(listenerFDs) != 1 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("parse seccomp listener fd: fds=%d err=%v", len(listenerFDs), err)
	}
	listenerFD := listenerFDs[0]
	defer unix.Close(listenerFD)
	pollFDs := []unix.PollFd{{Fd: int32(listenerFD), Events: unix.POLLIN}}
	n, err := unix.Poll(pollFDs, 5000)
	if err != nil || n != 1 || pollFDs[0].Revents&unix.POLLIN == 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("timed out waiting for blocked fsync notification: ready=%d revents=%#x err=%v", n, pollFDs[0].Revents, err)
	}

	var notification seccompNotification
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(listenerFD), uintptr(unix.SECCOMP_IOCTL_NOTIF_RECV), uintptr(unsafe.Pointer(&notification)))
	runtime.KeepAlive(&notification)
	if errno != 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("receive seccomp syscall notification: %v", errno)
	}
	threadPath := "/proc/" + strconv.Itoa(cmd.Process.Pid) + "/task/" + strconv.Itoa(int(notification.PID))
	if _, err := os.Stat(threadPath); err != nil || (notification.Data.Number != unix.SYS_FSYNC && notification.Data.Number != unix.SYS_FDATASYNC) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("unexpected blocked syscall: tid=%d task_exists=%v nr=%d", notification.PID, err == nil, notification.Data.Number)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("hard-kill child during blocked fsync: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("child exited successfully after SIGKILL")
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("child exit status=%v, want SIGKILL", cmd.ProcessState)
	}

	recovered, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	recoveryLease, err := recovered.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer recoveryLease.Close()
	lines, truncated, err := recovered.RecoverJSONLines("runs/demo/events.ndjson")
	if err != nil || truncated || len(lines) != 2 {
		t.Fatalf("recovery after SIGKILL during fsync: records=%d truncated=%v err=%v", len(lines), truncated, err)
	}
	if err := recovered.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 3}); err != nil {
		t.Fatalf("append after recovery: %v", err)
	}
}

func installFsyncNotificationFilter() (int, error) {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return -1, err
	}
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 2, K: uint32(unix.SYS_FSYNC)},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: uint32(unix.SYS_FDATASYNC)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_USER_NOTIF},
	}
	program := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	fd, _, errno := unix.Syscall6(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_NEW_LISTENER, uintptr(unsafe.Pointer(&program)), 0, 0, 0)
	runtime.KeepAlive(&program)
	runtime.KeepAlive(filter)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}
