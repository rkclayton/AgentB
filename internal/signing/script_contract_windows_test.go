//go:build windows

package signing

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPowerShellSigningScriptsUseHostCompatibleCodeSigningEKUCheck(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	for _, relative := range []string{"scripts/manage-signing.ps1", "scripts/install-Agent_b.ps1"} {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !strings.Contains(text, "([string]$_.ObjectId) -eq '1.3.6.1.5.5.7.3.3'") {
			t.Errorf("%s does not accept PowerShell's string-valued EnhancedKeyUsageList.ObjectId", relative)
		}
		if strings.Contains(text, "$_.ObjectId.Value -eq '1.3.6.1.5.5.7.3.3'") {
			t.Errorf("%s retains the host-incompatible ObjectId.Value check", relative)
		}
	}
	manager, err := os.ReadFile(filepath.Join(root, "scripts", "manage-signing.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(manager)
	for _, required := range []string{"Cert:\\LocalMachine\\My", "-KeyExportPolicy NonExportable", "-Verb RunAs"} {
		if !strings.Contains(text, required) {
			t.Errorf("manage-signing.ps1 does not preserve %q", required)
		}
	}
	if strings.Contains(text, "-KeyProtection ProtectHigh") {
		t.Error("self-created signing keys must be UAC-gated rather than password-prompted per signature")
	}
	installer, err := os.ReadFile(filepath.Join(root, "scripts", "install-Agent_b.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installer), "$SigningThumbprint -eq 'auto'") {
		t.Error("installer does not support first-install certificate bootstrap inside its existing elevation")
	}
	if !strings.Contains(string(installer), "-not $TestMode -and -not $SigningThumbprint") {
		t.Error("isolated TestMode installs must not use or create the production signing key")
	}
	for _, required := range []string{
		"Get-ChildItem -LiteralPath 'Cert:\\LocalMachine\\My'",
		"REUSED: administrator-gated signing certificate",
		"Add-CurrentUserCertificate -Certificate $certificate -StoreName TrustedPublisher",
		"Add-CurrentUserCertificate -Certificate $certificate -StoreName Root",
		"$PSVersionTable.PSEdition -ne 'Desktop'",
		"Push-Location $sourceRoot",
	} {
		if !strings.Contains(string(installer), required) {
			t.Errorf("installer does not preserve seamless bootstrap contract %q", required)
		}
	}
	if strings.Contains(string(installer), "Import-Certificate -FilePath $tempCertificate") {
		t.Error("installer retains interactive certificate import path")
	}
}
