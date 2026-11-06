//go:build windows

package executor

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func dialAgentEndpoint(endpoint string) (io.ReadWriteCloser, error) {
	if !strings.HasPrefix(strings.ToLower(endpoint), `\\.\pipe\`) || strings.Contains(endpoint, "..") {
		return nil, fmt.Errorf("Agent endpoint must be a local named pipe")
	}
	name, err := windows.UTF16PtrFromString(endpoint)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	if err := verifyNamedPipeServerUser(handle); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return os.NewFile(uintptr(handle), endpoint), nil
}

func verifyNamedPipeServerUser(pipe windows.Handle) error {
	var serverPID uint32
	if err := windows.GetNamedPipeServerProcessId(pipe, &serverPID); err != nil {
		return fmt.Errorf("cannot identify local Agent pipe server")
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, serverPID)
	if err != nil {
		return fmt.Errorf("cannot verify local Agent pipe server identity")
	}
	defer windows.CloseHandle(process)
	var serverToken windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &serverToken); err != nil {
		return fmt.Errorf("cannot inspect local Agent pipe server identity")
	}
	defer serverToken.Close()
	serverUser, err := serverToken.GetTokenUser()
	if err != nil {
		return fmt.Errorf("cannot inspect local Agent pipe server identity")
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("cannot inspect current Windows user identity")
	}
	serverSID := serverUser.User.Sid.String()
	currentSID := currentUser.User.Sid.String()
	// OpenSSH's Windows Agent commonly runs as LocalSystem. The pipe open above
	// already enforced its DACL for this caller; reject unrelated user processes.
	if !trustedNamedPipeServerSID(serverSID, currentSID) {
		return fmt.Errorf("Agent pipe server belongs to a different Windows user")
	}
	return nil
}

func trustedNamedPipeServerSID(serverSID, currentSID string) bool {
	return serverSID != "" && currentSID != "" && (serverSID == currentSID || serverSID == "S-1-5-18")
}
