//go:build !linux && !windows

package oplog

import (
	"fmt"
	"os"
)

func OpenFile(string) (*os.File, error) { return nil, fmt.Errorf("LOG_FILE_PLATFORM_UNAVAILABLE") }
