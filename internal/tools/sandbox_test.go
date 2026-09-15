package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/session"
)

func TestSandboxCapabilitySuiteStubRoutesOnlyDeclaredWorkspaceAndBash(t *testing.T) {
	stubDir := buildSBXStub(t)
	statePath := filepath.Join(t.TempDir(), "state")
	logPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SBX_STUB_STATE", statePath)
	t.Setenv("SBX_STUB_LOG", logPath)

	workspace := t.TempDir()
	repoA, repoB := t.TempDir(), t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-secret")
	cfg := config.Defaults(workspace)
	shell := NewShell(cfg.Shell)
	shell.Configure(cfg)
	item := &session.Session{ID: "sandbox", Role: "b", Workspace: workspace, PlanRepos: func() []string { return []string{repoA, repoB} }}
	if _, err := item.ReadRoot(filepath.Join(repoB, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := item.ReadRoot(filepath.Join(repoA, "a.txt")); err != nil {
		t.Fatal(err)
	}

	status := shell.SandboxStatus()
	if !status.Available || !status.Installed || !status.SignedIn || !strings.Contains(status.Version, "0.47-test") {
		t.Fatalf("status=%+v", status)
	}
	pending := shell.CallDetailed(context.Background(), item, map[string]any{"command": "uname -s"})
	if pending.OperatorOverrideReason == "" || pending.Metadata["target"] == "" {
		t.Fatalf("pending=%+v", pending)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("sandbox started before operator approval: %v", err)
	}
	content, err := shell.CallAsOperator(context.Background(), item, map[string]any{"command": "uname -s"})
	if err != nil || !strings.Contains(content, "target: sandbox agentb-") || !strings.Contains(content, "LINUX_ONLY_OK") {
		t.Fatalf("content=%q err=%v", content, err)
	}
	run := NewRunScript(shell)
	script, err := run.CallAsOperator(context.Background(), item, map[string]any{"language": "bash", "source": "printf sandbox-stdin-ok"})
	if err != nil || !strings.Contains(script, "sandbox-stdin-ok") {
		t.Fatalf("script=%q err=%v", script, err)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBytes)
	if strings.Count(logText, "create --name") != 1 || !strings.Contains(logText, " shell "+workspace+" "+repoA+" "+repoB) {
		t.Fatalf("create calls=%q", logText)
	}
	if strings.Contains(logText, outside) {
		t.Fatalf("outside host path reached sbx argv: %q", logText)
	}
	if !strings.Contains(logText, "exec -i agentb-") || !strings.Contains(logText, "stdin=printf sandbox-stdin-ok") {
		t.Fatalf("bash stdin call=%q", logText)
	}
}

func TestBashIsInertWithoutDeclaredReadySandbox(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.Defaults(workspace)
	cfg.Sandbox.Enabled = false
	shell := NewShell(cfg.Shell)
	shell.Configure(cfg)
	detail := NewRunScript(shell).CallDetailed(context.Background(), &session.Session{Workspace: workspace}, map[string]any{"language": "bash", "source": "uname -s"})
	if detail.Err == nil || !strings.Contains(detail.Err.Error(), "requires the global Docker Sandbox setting") {
		t.Fatalf("detail=%+v", detail)
	}
}

func TestUnavailableGlobalSandboxIsInertForHostShellAndExplainsBash(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.Defaults(workspace)
	shell := NewShell(cfg.Shell)
	shell.Configure(cfg)
	item := &session.Session{ID: "inert", Workspace: workspace}
	host := shell.CallDetailed(context.Background(), item, map[string]any{"command": "Write-Output host"})
	if host.Err != nil || !strings.Contains(host.Content, "host") {
		t.Fatalf("host fallback=%+v", host)
	}
	bash := NewRunScript(shell).CallDetailed(context.Background(), item, map[string]any{"language": "bash", "source": "true"})
	if bash.Err == nil || !strings.Contains(bash.Err.Error(), "enabled but inert") || !strings.Contains(bash.Err.Error(), "sbx is not installed") {
		t.Fatalf("bash=%+v", bash)
	}
}

func buildSBXStub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := `package main
import("fmt";"io";"os";"strings")
func main(){
 args:=os.Args[1:]
 if len(args)>0 && args[0]=="version" { fmt.Print("{\"client\":\"0.47-test\",\"server\":\"0.47-test\"}"); return }
 if len(args)>0 && args[0]=="ls" {
  if len(args)>1 && args[1]=="-q" { if b,e:=os.ReadFile(os.Getenv("SBX_STUB_STATE")); e==nil { fmt.Print(string(b)) }; return }
  fmt.Print("[]"); return
 }
 log:=os.Getenv("SBX_STUB_LOG")
 stdin,_:=io.ReadAll(os.Stdin)
 line:=strings.Join(args," ")+" stdin="+string(stdin)+"\n"
 f,_:=os.OpenFile(log,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600); if f!=nil { f.WriteString(line); f.Close() }
 if len(args)>0 && args[0]=="create" { for i,v:=range args { if v=="--name" && i+1<len(args) { os.WriteFile(os.Getenv("SBX_STUB_STATE"),[]byte(args[i+1]+"\n"),0600) } }; return }
 if len(args)>0 && args[0]=="exec" { fmt.Printf("LINUX_ONLY_OK %s",stdin); return }
 os.Exit(2)
}`
	sourcePath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(dir, "sbx")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	goExecutable := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goExecutable += ".exe"
	}
	command := exec.Command(goExecutable, "build", "-o", executable, sourcePath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build sbx stub: %v: %s", err, output)
	}
	return dir
}
