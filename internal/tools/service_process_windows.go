//go:build windows

package tools

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"harness/internal/config"
)

const (
	logonWithProfile         = 0x00000001
	createSuspended          = 0x00000004
	createNewProcessGroup    = 0x00000200
	createUnicodeEnvironment = 0x00000400
	createNoWindow           = 0x08000000
	startfUseStdHandles      = 0x00000100
	handleFlagInherit        = 0x00000001
	infinite                 = 0xffffffff
	stillActive              = 259
	errorLogonFailure        = syscall.Errno(1326)
	errorLogonTypeNotGranted = syscall.Errno(1385)
	errorDirectory           = syscall.Errno(267)
)

var (
	advapi32                    = syscall.NewLazyDLL("advapi32.dll")
	procCreateProcessWithLogonW = advapi32.NewProc("CreateProcessWithLogonW")
	serviceKernel32             = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW        = serviceKernel32.NewProc("CreateJobObjectW")
	procAssignProcessToJob      = serviceKernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject      = serviceKernel32.NewProc("TerminateJobObject")
	procResumeThread            = serviceKernel32.NewProc("ResumeThread")
	serviceSpawnMu              sync.Mutex
)

type serviceShellProcess struct {
	process  syscall.Handle
	job      syscall.Handle
	output   *os.File
	copyDone chan struct{}
	once     sync.Once
}

func startServiceAccountProcess(executable string, argv []string, workspace string, environment []string, account config.ShellServiceAccount, password []byte, output *lockedBuffer) (runningShellProcess, error) {
	qualified := account.Account
	if account.Domain != "" && account.Domain != "." {
		qualified = account.Domain + `\` + account.Account
	}
	if _, _, _, err := syscall.LookupSID("", qualified); err != nil {
		return nil, &serviceSpawnError{kind: fmt.Sprintf("service account %q does not exist", qualified), err: err}
	}

	resolved, err := exec.LookPath(executable)
	if err != nil {
		return nil, &serviceSpawnError{kind: "service-account shell executable was not found", err: err}
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, &serviceSpawnError{kind: "service-account shell executable path could not be resolved", err: err}
	}

	serviceSpawnMu.Lock()
	defer serviceSpawnMu.Unlock()

	var security syscall.SecurityAttributes
	security.Length = uint32(unsafe.Sizeof(security))
	security.InheritHandle = 1
	var stdinRead, stdinWrite, outputRead, outputWrite syscall.Handle
	if err := syscall.CreatePipe(&stdinRead, &stdinWrite, &security, 0); err != nil {
		return nil, &serviceSpawnError{kind: "service-account standard-input pipe creation failed", err: err}
	}
	defer syscall.CloseHandle(stdinRead)
	defer syscall.CloseHandle(stdinWrite)
	if err := syscall.CreatePipe(&outputRead, &outputWrite, &security, 0); err != nil {
		return nil, &serviceSpawnError{kind: "service-account output pipe creation failed", err: err}
	}
	defer func() {
		if outputRead != 0 {
			syscall.CloseHandle(outputRead)
		}
		if outputWrite != 0 {
			syscall.CloseHandle(outputWrite)
		}
	}()
	if err := syscall.SetHandleInformation(stdinWrite, handleFlagInherit, 0); err != nil {
		return nil, &serviceSpawnError{kind: "service-account input pipe setup failed", err: err}
	}
	if err := syscall.SetHandleInformation(outputRead, handleFlagInherit, 0); err != nil {
		return nil, &serviceSpawnError{kind: "service-account output pipe setup failed", err: err}
	}
	// Closing the parent's write end makes the child's inherited stdin read as EOF.
	syscall.CloseHandle(stdinWrite)
	stdinWrite = 0

	userPtr, err := syscall.UTF16PtrFromString(account.Account)
	if err != nil {
		return nil, &serviceSpawnError{kind: "service-account name is invalid", err: err}
	}
	domainPtr, err := syscall.UTF16PtrFromString(account.Domain)
	if err != nil {
		return nil, &serviceSpawnError{kind: "service-account domain is invalid", err: err}
	}
	applicationPtr, err := syscall.UTF16PtrFromString(resolved)
	if err != nil {
		return nil, &serviceSpawnError{kind: "service-account shell executable path is invalid", err: err}
	}
	directoryPtr, err := syscall.UTF16PtrFromString(workspace)
	if err != nil {
		return nil, &serviceSpawnError{kind: "service-account working directory is invalid", err: err}
	}
	commandLine, err := syscall.UTF16FromString(joinWindowsCommandLine(resolved, argv))
	if err != nil {
		return nil, &serviceSpawnError{kind: "service-account command is invalid", err: err}
	}
	environmentBlock := append(utf16.Encode([]rune(strings.Join(environment, "\x00"))), 0, 0)
	passwordUTF16 := append(utf16.Encode(bytes.Runes(password)), 0)
	defer func() {
		for i := range passwordUTF16 {
			passwordUTF16[i] = 0
		}
	}()

	startup := syscall.StartupInfo{Cb: uint32(unsafe.Sizeof(syscall.StartupInfo{})), Flags: startfUseStdHandles, StdInput: stdinRead, StdOutput: outputWrite, StdErr: outputWrite}
	var process syscall.ProcessInformation
	result, _, callErr := procCreateProcessWithLogonW.Call(
		uintptr(unsafe.Pointer(userPtr)), uintptr(unsafe.Pointer(domainPtr)), uintptr(unsafe.Pointer(&passwordUTF16[0])), logonWithProfile,
		uintptr(unsafe.Pointer(applicationPtr)), uintptr(unsafe.Pointer(&commandLine[0])),
		createSuspended|createNewProcessGroup|createUnicodeEnvironment|createNoWindow,
		uintptr(unsafe.Pointer(&environmentBlock[0])), uintptr(unsafe.Pointer(directoryPtr)),
		uintptr(unsafe.Pointer(&startup)), uintptr(unsafe.Pointer(&process)),
	)
	runtime.KeepAlive(userPtr)
	runtime.KeepAlive(domainPtr)
	runtime.KeepAlive(applicationPtr)
	runtime.KeepAlive(directoryPtr)
	runtime.KeepAlive(commandLine)
	runtime.KeepAlive(environmentBlock)
	runtime.KeepAlive(passwordUTF16)
	runtime.KeepAlive(startup)
	if result == 0 {
		return nil, classifyLogonFailure(callErr)
	}
	defer syscall.CloseHandle(process.Thread)

	job, _, jobErr := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		syscall.TerminateProcess(process.Process, 1)
		syscall.CloseHandle(process.Process)
		return nil, &serviceSpawnError{kind: "service-account job object creation failed", err: jobErr}
	}
	assigned, _, assignErr := procAssignProcessToJob.Call(job, uintptr(process.Process))
	if assigned == 0 {
		syscall.TerminateProcess(process.Process, 1)
		syscall.CloseHandle(process.Process)
		syscall.CloseHandle(syscall.Handle(job))
		return nil, &serviceSpawnError{kind: "service-account process-tree job assignment failed", err: assignErr}
	}
	resumed, _, resumeErr := procResumeThread.Call(uintptr(process.Thread))
	if resumed == 0xffffffff {
		procTerminateJobObject.Call(job, 1)
		syscall.CloseHandle(process.Process)
		syscall.CloseHandle(syscall.Handle(job))
		return nil, &serviceSpawnError{kind: "service-account process resume failed", err: resumeErr}
	}

	// Only the child keeps the write handles. The reader receives EOF when the tree exits.
	syscall.CloseHandle(stdinRead)
	stdinRead = 0
	syscall.CloseHandle(outputWrite)
	outputWrite = 0
	outputFile := os.NewFile(uintptr(outputRead), "service-shell-output")
	outputRead = 0
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, outputFile)
		close(done)
	}()
	return &serviceShellProcess{process: process.Process, job: syscall.Handle(job), output: outputFile, copyDone: done}, nil
}

func (p *serviceShellProcess) Wait() (int, error) {
	wait, err := syscall.WaitForSingleObject(p.process, infinite)
	if err != nil || wait != syscall.WAIT_OBJECT_0 {
		p.close()
		if err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("unexpected process wait result %d", wait)
	}
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(p.process, &exitCode); err != nil {
		p.close()
		return 0, err
	}
	if exitCode == stillActive {
		p.close()
		return 0, fmt.Errorf("process remained active after wait")
	}
	<-p.copyDone
	p.close()
	return int(exitCode), nil
}

func (p *serviceShellProcess) KillTree() {
	if result, _, _ := procTerminateJobObject.Call(uintptr(p.job), 1); result == 0 {
		_ = syscall.TerminateProcess(p.process, 1)
	}
}

func (p *serviceShellProcess) close() {
	p.once.Do(func() {
		_ = p.output.Close()
		_ = syscall.CloseHandle(p.process)
		_ = syscall.CloseHandle(p.job)
	})
}

func minimalShellEnvironment(account config.ShellServiceAccount, workspace string) []string {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	domain := account.Domain
	if domain == "." {
		domain = os.Getenv("COMPUTERNAME")
	}
	values := map[string]string{
		"COMSPEC":     filepath.Join(systemRoot, "System32", "cmd.exe"),
		"HOMEDRIVE":   filepath.VolumeName(workspace),
		"HOMEPATH":    `\`,
		"PATH":        strings.Join([]string{filepath.Join(systemRoot, "System32"), systemRoot, filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0")}, ";"),
		"PATHEXT":     `.COM;.EXE;.BAT;.CMD`,
		"SYSTEMDRIVE": filepath.VolumeName(systemRoot),
		"SYSTEMROOT":  systemRoot,
		"TEMP":        workspace,
		"TMP":         workspace,
		"USERDOMAIN":  domain,
		"USERNAME":    account.Account,
	}
	for _, name := range []string{"LANG", "LC_ALL", "LC_CTYPE", "NO_COLOR"} {
		if value := os.Getenv(name); value != "" {
			values[name] = value
		}
	}
	environment := make([]string, 0, len(values))
	for name, value := range values {
		environment = append(environment, name+"="+value)
	}
	sort.Slice(environment, func(i, j int) bool { return strings.ToLower(environment[i]) < strings.ToLower(environment[j]) })
	return environment
}

func classifyLogonFailure(err error) error {
	if errno, ok := err.(syscall.Errno); ok {
		switch errno {
		case errorLogonFailure:
			return &serviceSpawnError{kind: "service-account authentication failed", err: errno}
		case errorLogonTypeNotGranted:
			return &serviceSpawnError{kind: "service account lacks the required logon right", err: errno}
		case errorDirectory:
			return &serviceSpawnError{kind: "service account cannot access the configured workspace", err: errno}
		}
	}
	return &serviceSpawnError{kind: "CreateProcessWithLogonW failed", err: err}
}

func joinWindowsCommandLine(executable string, argv []string) string {
	parts := make([]string, 0, len(argv)+1)
	parts = append(parts, syscall.EscapeArg(executable))
	for _, arg := range argv {
		parts = append(parts, syscall.EscapeArg(arg))
	}
	return strings.Join(parts, " ")
}
