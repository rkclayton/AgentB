//go:build !windows

package tools

import (
	"fmt"

	"harness/internal/config"
)

func runAsServiceFileIdentity(config.ShellServiceAccount, []byte, func() (string, error)) (string, error) {
	return "", &serviceFileIdentityError{err: fmt.Errorf("service-account file identity is supported only on Windows")}
}
