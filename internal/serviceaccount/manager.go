package serviceaccount

import (
	"context"
	"errors"
)

var ErrUnsupported = errors.New("service-account setup is supported only on Windows")

type Status struct {
	Supported       bool   `json:"supported"`
	Account         string `json:"account"`
	Exists          bool   `json:"exists"`
	Enabled         bool   `json:"enabled"`
	Administrator   bool   `json:"administrator"`
	HarnessElevated bool   `json:"harness_elevated"`
}

type SetupResult struct {
	// Attempted is true once the elevated setup script started. A failure after
	// that point can be a partial account/password change and must not be
	// reported as a harmless UAC cancellation.
	Attempted bool
}

type Manager interface {
	Status(context.Context, string) (Status, error)
	Setup(context.Context, string, string, bool) (SetupResult, error)
}
