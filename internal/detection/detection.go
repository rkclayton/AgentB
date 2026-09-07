package detection

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func Local(ctx context.Context, scriptPath, serviceAccount string) (any, error) {
	if runtime.GOOS != "windows" {
		return map[string]any{"supported": false, "reason": "local detection is available on Windows"}, nil
	}
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	command := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", scriptPath, "-ServiceAccount", serviceAccount)
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("local detection failed: %w", err)
	}
	var report any
	if err := json.Unmarshal(output, &report); err != nil {
		return nil, fmt.Errorf("decode local detection report: %w", err)
	}
	return report, nil
}
