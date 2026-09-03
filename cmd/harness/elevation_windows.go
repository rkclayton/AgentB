//go:build windows

package main

import "syscall"

var procIsUserAnAdmin = syscall.NewLazyDLL("shell32.dll").NewProc("IsUserAnAdmin")

// processIsElevated checks the effective token, not whether the account is a
// member of Administrators. UAC-filtered local administrators return false.
func processIsElevated() bool {
	result, _, _ := procIsUserAnAdmin.Call()
	return result != 0
}
