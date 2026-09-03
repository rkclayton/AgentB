package hardening

import (
	"context"
	"errors"
)

var ErrUnsupported = errors.New("AgentB host hardening is supported only on Windows")

type Request struct {
	AccountName        string
	HarnessDirectory   string
	WorkspaceDirectory string
	ModelAddress       string
	ModelPort          int
}

type ComponentStatus struct {
	Supported     bool   `json:"supported"`
	AccountExists bool   `json:"account_exists"`
	Applied       bool   `json:"applied"`
	Drift         int    `json:"drift,omitempty"`
	Summary       string `json:"summary"`
}

type Status struct {
	Supported       bool            `json:"supported"`
	HarnessElevated bool            `json:"harness_elevated"`
	ModelAddress    string          `json:"model_address"`
	ModelPort       int             `json:"model_port"`
	ACL             ComponentStatus `json:"acl"`
	Firewall        ComponentStatus `json:"firewall"`
	Applied         bool            `json:"applied"`
}

type RunResult struct {
	Attempted bool
}

type Manager interface {
	Status(context.Context, Request) (Status, error)
	Run(context.Context, string, Request) (RunResult, error)
}
