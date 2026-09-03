//go:build windows

package tools

import (
	"fmt"
	"syscall"
	"unsafe"
)

func atomicReplace(source, target string) error {
	s, _ := syscall.UTF16PtrFromString(source)
	t, _ := syscall.UTF16PtrFromString(target)
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")
	ok, _, callErr := proc.Call(uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(t)), uintptr(0x1|0x8))
	if ok == 0 {
		return fmt.Errorf("atomic replace: %v", callErr)
	}
	return nil
}
