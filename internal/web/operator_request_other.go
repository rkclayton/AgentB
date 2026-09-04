//go:build !windows

package web

import (
	"fmt"
	"net/http"
)

func requireOperatorHTTPClient(*http.Request) error {
	return fmt.Errorf("operator-context client identity verification is supported only on Windows")
}
