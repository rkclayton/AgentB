//go:build !windows

package agent

import (
	"fmt"
	"time"
)

func operatingSystemContext() string {
	zone, offset := time.Now().Zone()
	return fmt.Sprintf("timezone %s (UTC%+d)", zone, offset/3600)
}
