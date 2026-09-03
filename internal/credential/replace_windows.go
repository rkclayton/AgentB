//go:build windows

package credential

import (
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

var procMoveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceFile(source, destination string) error {
	sourcePtr, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	result, _, callErr := procMoveFileExW.Call(
		uintptr(unsafe.Pointer(sourcePtr)),
		uintptr(unsafe.Pointer(destinationPtr)),
		moveFileReplaceExisting|moveFileWriteThrough,
	)
	if result == 0 {
		return callErr
	}
	return nil
}
