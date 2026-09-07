//go:build windows

package web

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

func nativeFolderPicker(initial string) (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms; $d=New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description='Bind new Agent_b chat to a workspace'; $d.SelectedPath=$args[0]; if($d.ShowDialog() -eq 'OK'){[Console]::Out.Write($d.SelectedPath)}else{exit 2}`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", script, initial)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("folder selection canceled")
	}
	selected := strings.TrimSpace(string(out))
	if selected == "" {
		return "", fmt.Errorf("folder selection canceled")
	}
	return selected, nil
}
