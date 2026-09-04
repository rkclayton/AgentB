//go:build windows

package agent

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/registry"
)

func operatingSystemContext() string {
	region := "unknown region"
	key, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\International\Geo`, registry.QUERY_VALUE)
	if err == nil {
		if name, _, readErr := key.GetStringValue("Name"); readErr == nil && name != "" {
			region = name
		}
		key.Close()
	}
	zone, offset := time.Now().Zone()
	return fmt.Sprintf("region %s; timezone %s (UTC%+d)", region, zone, offset/3600)
}
