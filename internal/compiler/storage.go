package compiler

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/fleeto/readyspec/internal/plan"
)

func filesystemCommand(values map[string]plan.ResolvedParam) string {
	path := shellQuote(values["path"].Value.(string))
	// GNU stat exposes f_bavail separately from f_bfree. Read-only statistics
	// avoid probe files and preserve reserved blocks instead of counting them
	// as space available to a nonprivileged deployment process.
	return `LC_ALL=C stat -f --printf '%S\n%b\n%f\n%a\n%c\n%d\n%T\n' -- ` + path + ` || exit 2; LC_ALL=C stat --printf '%m\n' -- ` + path + ` || exit 2; printf '__RS_MOUNTINFO__\n'; cat /proc/self/mountinfo || exit 2`
}

func filesystemEvaluation(id string, params map[string]any, text string) (string, map[string]any) {
	summary := map[string]any{}
	parts := strings.Split(text, "__RS_MOUNTINFO__\n")
	if len(parts) != 2 {
		return "ERROR", summary
	}
	fields := strings.Split(strings.TrimSuffix(parts[0], "\n"), "\n")
	if len(fields) != 8 {
		return "ERROR", summary
	}
	numbers := make([]*big.Int, 6)
	for i := range numbers {
		value, ok := new(big.Int).SetString(fields[i], 10)
		if !ok || value.Sign() < 0 {
			return "ERROR", summary
		}
		numbers[i] = value
	}
	blockSize, totalBlocks, freeBlocks, availableBlocks, totalInodes, availableInodes := numbers[0], numbers[1], numbers[2], numbers[3], numbers[4], numbers[5]
	if blockSize.Sign() == 0 || freeBlocks.Cmp(totalBlocks) > 0 || availableBlocks.Cmp(freeBlocks) > 0 || availableInodes.Cmp(totalInodes) > 0 || fields[6] == "" || !strings.HasPrefix(fields[7], "/") {
		return "ERROR", summary
	}
	filesystem, source, mountRoot, mountPoint := "", "", "", ""
	path := fmt.Sprint(params["path"])
	if !strings.HasPrefix(path, "/") {
		return "ERROR", summary
	}
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	bestLength := -1
	for _, line := range strings.Split(parts[1], "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		before, after, ok := strings.Cut(line, " - ")
		if !ok {
			return "ERROR", summary
		}
		mountFields, fsFields := strings.Fields(before), strings.Fields(after)
		if len(mountFields) < 6 || len(fsFields) < 3 {
			return "ERROR", summary
		}
		candidate := decode.Replace(mountFields[4])
		if !mountPointContains(candidate, path) || len(candidate) < bestLength {
			continue
		}
		// Later entries overmount earlier ones on the same target, so take the
		// last longest prefix instead of trusting stat's device-based %m.
		bestLength = len(candidate)
		mountPoint = candidate
		filesystem = fsFields[0]
		source = decode.Replace(fsFields[1])
		mountRoot = decode.Replace(mountFields[3])
	}
	if filesystem == "" || source == "" || mountRoot == "" {
		return "ERROR", summary
	}
	toBytes := func(blocks *big.Int) *big.Int { return new(big.Int).Mul(blockSize, blocks) }
	summary["block_size_bytes"] = json.Number(blockSize.String())
	summary["total_bytes"] = json.Number(toBytes(totalBlocks).String())
	summary["free_bytes"] = json.Number(toBytes(freeBlocks).String())
	summary["available_bytes"] = json.Number(toBytes(availableBlocks).String())
	summary["total_inodes"] = json.Number(totalInodes.String())
	summary["available_inodes"] = json.Number(availableInodes.String())
	summary["filesystem"] = filesystem
	summary["filesystem_family"] = fields[6]
	summary["source"] = source
	summary["mount_root"] = mountRoot
	summary["mount_point"] = mountPoint
	summary["stat_mount_point"] = fields[7]
	if totalInodes.Sign() > 0 {
		percent := new(big.Rat).SetFrac(new(big.Int).Mul(availableInodes, big.NewInt(100)), totalInodes)
		summary["free_percent"] = json.Number(percent.FloatString(12))
	}
	if id == "S04" {
		return exactNumericMatch(map[string]any{"ge": params["min_available"]}, toBytes(availableBlocks).String(), false), summary
	}
	if totalInodes.Sign() == 0 {
		return "ERROR", summary
	}
	if minimum, ok := params["min_available"]; ok {
		return exactNumericMatch(map[string]any{"ge": minimum}, availableInodes.String(), false), summary
	}
	minimum, ok := new(big.Rat).SetString(fmt.Sprint(params["min_percent"]))
	if !ok {
		return "ERROR", summary
	}
	actual := new(big.Rat).SetFrac(new(big.Int).Mul(availableInodes, big.NewInt(100)), totalInodes)
	if actual.Cmp(minimum) >= 0 {
		return "PASS", summary
	}
	return "FAIL", summary
}

// mountPointContains reports whether path lives under the mount point. The
// comparison respects path component boundaries so "/bound" does not match
// "/boundary".
func mountPointContains(mountPoint, path string) bool {
	if mountPoint == "/" {
		return true
	}
	return path == mountPoint || strings.HasPrefix(path, strings.TrimSuffix(mountPoint, "/")+"/")
}
