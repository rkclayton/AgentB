//go:build windows

package tools

import (
	"bytes"
	"fmt"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"harness/internal/config"
)

const (
	logon32LogonInteractive = 2
	logon32ProviderDefault  = 0
)

var (
	fileAdvapi32                = syscall.NewLazyDLL("advapi32.dll")
	procLogonUserW              = fileAdvapi32.NewProc("LogonUserW")
	procImpersonateLoggedOnUser = fileAdvapi32.NewProc("ImpersonateLoggedOnUser")
	procRevertToSelf            = fileAdvapi32.NewProc("RevertToSelf")
)

func runAsServiceFileIdentity(account config.ShellServiceAccount, password []byte, call func() (string, error)) (result string, err error) {
	user, err := syscall.UTF16PtrFromString(account.Account)
	if err != nil {
		return "", &serviceFileIdentityError{err: fmt.Errorf("service-account name is invalid: %w", err)}
	}
	domain, err := syscall.UTF16PtrFromString(account.Domain)
	if err != nil {
		return "", &serviceFileIdentityError{err: fmt.Errorf("service-account domain is invalid: %w", err)}
	}
	passwordUTF16 := append(utf16.Encode(bytes.Runes(password)), 0)
	defer func() {
		for i := range passwordUTF16 {
			passwordUTF16[i] = 0
		}
	}()
	var token syscall.Handle
	loggedOn, _, logonErr := procLogonUserW.Call(
		uintptr(unsafe.Pointer(user)),
		uintptr(unsafe.Pointer(domain)),
		uintptr(unsafe.Pointer(&passwordUTF16[0])),
		logon32LogonInteractive,
		logon32ProviderDefault,
		uintptr(unsafe.Pointer(&token)),
	)
	runtime.KeepAlive(user)
	runtime.KeepAlive(domain)
	runtime.KeepAlive(passwordUTF16)
	if loggedOn == 0 {
		return "", &serviceFileIdentityError{err: classifyLogonFailure(logonErr)}
	}
	defer syscall.CloseHandle(token)

	runtime.LockOSThread()
	impersonated, _, impersonateErr := procImpersonateLoggedOnUser.Call(uintptr(token))
	if impersonated == 0 {
		runtime.UnlockOSThread()
		return "", &serviceFileIdentityError{err: fmt.Errorf("impersonate service-account file identity: %w", impersonateErr)}
	}
	defer func() {
		ok, _, revertErr := procRevertToSelf.Call()
		if ok == 0 {
			if err == nil {
				err = &serviceFileIdentityError{err: fmt.Errorf("revert service-account file identity: %w", revertErr)}
			}
			return
		}
		runtime.UnlockOSThread()
	}()
	result, err = call()
	return result, err
}
