//go:build windows

package credential

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

const cryptProtectUIForbidden = 0x1

var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree          = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	size uint32
	data *byte
}

func protect(plain []byte) ([]byte, error) {
	return cryptData(procCryptProtectData, plain)
}

func unprotect(protected []byte) ([]byte, error) {
	return cryptData(procCryptUnprotectData, protected)
}

func cryptData(proc *syscall.LazyProc, input []byte) ([]byte, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("DPAPI input is empty")
	}
	in := dataBlob{size: uint32(len(input)), data: &input[0]}
	var out dataBlob
	result, _, callErr := proc.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	runtime.KeepAlive(input)
	if result == 0 {
		return nil, fmt.Errorf("DPAPI call failed: %w", callErr)
	}
	if out.data == nil || out.size == 0 {
		return nil, fmt.Errorf("DPAPI returned an empty result")
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return append([]byte(nil), unsafe.Slice(out.data, out.size)...), nil
}
