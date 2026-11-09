package executor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

type remoteDirectory struct {
	Exists     bool
	Writable   bool
	Executable bool
	Symlink    bool
	MountID    string
}

type workspacePaths struct {
	Standard   string
	Privileged string
}

func discoverWorkspacePaths(ctx context.Context, client *ssh.Client, explicit string, needsPrivileged bool) (workspacePaths, error) {
	if explicit != "" && (!path.IsAbs(explicit) || path.Clean(explicit) != explicit || strings.ContainsAny(explicit, "\x00\r\n\t")) {
		return workspacePaths{}, errors.New("configured remote temp directory must be a clean absolute path")
	}
	command := workspaceProbeScript(explicit)
	output, err := runSSH(ctx, client, command, nil)
	if err != nil {
		return workspacePaths{}, err
	}
	directories := map[string]remoteDirectory{}
	mounts := make([]string, 0)
	home := ""
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "HOME=") {
			home = strings.TrimPrefix(line, "HOME=")
			continue
		}
		if strings.HasPrefix(line, "MOUNT\t") {
			mounts = append(mounts, strings.TrimPrefix(line, "MOUNT\t"))
			continue
		}
		if !strings.HasPrefix(line, "DIR\t") {
			continue
		}
		fields := strings.Split(line, "\t")
		if (len(fields) != 6 && len(fields) != 7) || fields[1] == "" || strings.ContainsAny(fields[1], "\x00\r\n\t") {
			return workspacePaths{}, errors.New("remote temp directory facts are malformed")
		}
		values := make([]bool, 4)
		for i := range values {
			value, parseErr := strconv.ParseBool(fields[i+2])
			if parseErr != nil {
				return workspacePaths{}, errors.New("remote temp directory facts are malformed")
			}
			values[i] = value
		}
		mountID := ""
		if len(fields) == 7 {
			mountID = fields[6]
		}
		directories[fields[1]] = remoteDirectory{Exists: values[0], Writable: values[1], Executable: values[2], Symlink: values[3], MountID: mountID}
	}
	if len(mounts) == 0 {
		return workspacePaths{}, errors.New("remote mount facts are unavailable")
	}
	standardCandidates := candidatePaths(explicit, "/tmp", "/var/tmp")
	if home != "" && !strings.ContainsAny(home, "\x00\r\n\t") {
		standardCandidates = append(standardCandidates, path.Join(home, ".cache"))
	}
	result := workspacePaths{}
	result.Standard, err = selectWorkspaceBase(standardCandidates, directories, mounts, true)
	if err != nil {
		return workspacePaths{}, fmt.Errorf("no safe STANDARD remote temp directory: %w", err)
	}
	if needsPrivileged {
		privilegedCandidates := candidatePaths(explicit, "/run", "/var/tmp", "/tmp")
		result.Privileged, err = selectWorkspaceBase(privilegedCandidates, directories, mounts, false)
		if err != nil {
			return workspacePaths{}, fmt.Errorf("no safe PRIVILEGED remote temp directory: %w", err)
		}
	}
	return result, nil
}

func candidatePaths(explicit string, defaults ...string) []string {
	result := make([]string, 0, len(defaults)+1)
	if explicit != "" {
		result = append(result, explicit)
	}
	result = append(result, defaults...)
	return result
}

func selectWorkspaceBase(candidates []string, directories map[string]remoteDirectory, mounts []string, requireWritable bool) (string, error) {
	for _, candidate := range candidates {
		if !path.IsAbs(candidate) || path.Clean(candidate) != candidate || strings.ContainsAny(candidate, "\x00\r\n\t") {
			continue
		}
		facts, ok := directories[candidate]
		if !ok || !facts.Exists || facts.Symlink || !facts.Executable || (requireWritable && !facts.Writable) {
			continue
		}
		options, ok := mountOptionsFor(candidate, mounts, facts.MountID)
		if !ok || hasMountOption(options, "ro") || hasMountOption(options, "noexec") {
			continue
		}
		return candidate, nil
	}
	return "", errors.New("no candidate directory is writable and executable")
}

func mountOptionsFor(candidate string, mounts []string, visibleMountID ...string) (string, bool) {
	candidate = path.Clean(candidate)
	if len(visibleMountID) > 0 && visibleMountID[0] != "" {
		for _, line := range mounts {
			fields := strings.Fields(line)
			if len(fields) >= 6 && fields[0] == visibleMountID[0] {
				mountPoint := path.Clean(decodeMountField(fields[4]))
				if candidate != mountPoint && !strings.HasPrefix(candidate, strings.TrimSuffix(mountPoint, "/")+"/") {
					return "", false
				}
				return fields[5], true
			}
		}
		return "", false
	}
	// Legacy peer without fdinfo mount IDs: combine restrictions across
	// same-path records so an overmount can never hide a noexec/ro flag.
	var visibleOptions string
	found := false
	for current := candidate; ; current = path.Dir(current) {
		for _, line := range mounts {
			fields := strings.Fields(line)
			if len(fields) < 6 {
				continue
			}
			mountPoint := decodeMountField(fields[4])
			if mountPoint == current {
				if !found {
					visibleOptions, found = fields[5], true
				} else {
					for _, restriction := range []string{"ro", "noexec"} {
						if hasMountOption(fields[5], restriction) && !hasMountOption(visibleOptions, restriction) {
							visibleOptions += "," + restriction
						}
					}
				}
			}
		}
		if found {
			return visibleOptions, true
		}
		parent := path.Dir(current)
		if parent == current {
			break
		}
	}
	return "", false
}

func decodeMountField(value string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(value)
}

func hasMountOption(options, expected string) bool {
	for _, option := range strings.Split(options, ",") {
		if option == expected {
			return true
		}
	}
	return false
}

func workspaceProbeScript(explicit string) string {
	var builder strings.Builder
	builder.WriteString("explicit=")
	builder.WriteString(shellQuote(explicit))
	builder.WriteString(`
probe_dir() {
  dir=$1
  exists=false; writable=false; executable=false; symlink=false
  test -d "$dir" && exists=true
  test -w "$dir" && writable=true
  test -x "$dir" && executable=true
  test -L "$dir" && symlink=true
  mount_id=$(
    (exec 3< "$dir" || exit 1
     while IFS= read -r line; do case "$line" in mnt_id:*) set -- $line; printf '%s' "$2"; break;; esac; done < /proc/self/fdinfo/3) 2>/dev/null
  ) || mount_id=
  case "$mount_id" in ''|*[!0-9]*) mount_id=;; esac
  printf 'DIR\t%s\t%s\t%s\t%s\t%s\t%s\n' "$dir" "$exists" "$writable" "$executable" "$symlink" "$mount_id"
}
if test -n "$explicit"; then probe_dir "$explicit"; fi
probe_dir /tmp
probe_dir /var/tmp
probe_dir /run
if test -n "$HOME"; then probe_dir "$HOME/.cache"; fi
printf 'HOME=%s\n' "$HOME"
while IFS= read -r mount; do printf 'MOUNT\t%s\n' "$mount"; done < /proc/self/mountinfo
`)
	return builder.String()
}
