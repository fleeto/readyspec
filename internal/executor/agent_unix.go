//go:build !windows

package executor

import (
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

func dialAgentEndpoint(endpoint string) (io.ReadWriteCloser, error) {
	info, err := os.Lstat(endpoint)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 {
		return nil, os.ErrInvalid
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0077 != 0 {
		return nil, os.ErrPermission
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	return dialer.Dial("unix", endpoint)
}
