//go:build windows

package ocr

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

type guid struct {
	data1 uint32
	data2 uint16
	data3 uint16
	data4 [8]byte
}

type inspectable struct{ vtable *[64]uintptr }

var (
	combase                  = syscall.NewLazyDLL("combase.dll")
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	shcore                   = syscall.NewLazyDLL("shcore.dll")
	roInitialize             = combase.NewProc("RoInitialize")
	roUninitialize           = combase.NewProc("RoUninitialize")
	windowsCreateString      = combase.NewProc("WindowsCreateString")
	windowsDeleteString      = combase.NewProc("WindowsDeleteString")
	windowsGetStringBuffer   = combase.NewProc("WindowsGetStringRawBuffer")
	roGetActivationFactory   = combase.NewProc("RoGetActivationFactory")
	rtlMoveMemory            = kernel32.NewProc("RtlMoveMemory")
	createRandomAccessOnFile = shcore.NewProc("CreateRandomAccessStreamOnFile")
	iidOcrEngineStatics      = guid{0x5bffa85a, 0x3384, 0x3540, [8]byte{0x99, 0x40, 0x69, 0x91, 0x20, 0xd4, 0x28, 0xa8}}
	iidBitmapDecoderStatics  = guid{0x438ccb26, 0xbcef, 0x4e95, [8]byte{0xba, 0xd6, 0x23, 0xa8, 0x22, 0xe5, 0x8d, 0x01}}
	iidRandomAccessStream    = guid{0x905a0fe1, 0xbc53, 0x11df, [8]byte{0x8c, 0x49, 0x00, 0x1e, 0x4f, 0xc6, 0x86, 0xda}}
	iidBitmapFrameSoftware   = guid{0xfe287c9a, 0x420c, 0x4963, [8]byte{0x87, 0xad, 0x69, 0x14, 0x36, 0xe0, 0x83, 0x83}}
	iidAsyncInfo             = guid{0x00000036, 0x0000, 0x0000, [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

func failed(hr uintptr) bool { return int32(hr) < 0 }

func release(value *inspectable) {
	if value != nil {
		syscall.SyscallN(value.vtable[2], uintptr(unsafe.Pointer(value)))
	}
}

func activationFactory(name string, iid *guid) (*inspectable, error) {
	encoded := utf16.Encode([]rune(name))
	var className uintptr
	hr, _, _ := windowsCreateString.Call(uintptr(unsafe.Pointer(&encoded[0])), uintptr(len(encoded)), uintptr(unsafe.Pointer(&className)))
	if failed(hr) {
		return nil, fmt.Errorf("create Windows Runtime class name: HRESULT 0x%08x", uint32(hr))
	}
	defer windowsDeleteString.Call(className)
	var factory *inspectable
	hr, _, _ = roGetActivationFactory.Call(className, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&factory)))
	if failed(hr) {
		return nil, fmt.Errorf("activate %s: HRESULT 0x%08x", name, uint32(hr))
	}
	return factory, nil
}

func query(value *inspectable, iid *guid) (*inspectable, error) {
	var result *inspectable
	hr, _, _ := syscall.SyscallN(value.vtable[0], uintptr(unsafe.Pointer(value)), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&result)))
	if failed(hr) {
		return nil, fmt.Errorf("query Windows Runtime interface: HRESULT 0x%08x", uint32(hr))
	}
	return result, nil
}

func asyncResult(operation *inspectable) (*inspectable, error) {
	info, err := query(operation, &iidAsyncInfo)
	if err != nil {
		return nil, err
	}
	defer release(info)
	deadline := time.Now().Add(30 * time.Second)
	for {
		var status uint32
		hr, _, _ := syscall.SyscallN(info.vtable[7], uintptr(unsafe.Pointer(info)), uintptr(unsafe.Pointer(&status)))
		if failed(hr) {
			return nil, fmt.Errorf("read Windows Runtime operation status: HRESULT 0x%08x", uint32(hr))
		}
		switch status {
		case 0:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("Windows Runtime operation timed out")
			}
			time.Sleep(10 * time.Millisecond)
		case 1:
			var result *inspectable
			hr, _, _ = syscall.SyscallN(operation.vtable[8], uintptr(unsafe.Pointer(operation)), uintptr(unsafe.Pointer(&result)))
			if failed(hr) {
				return nil, fmt.Errorf("finish Windows Runtime operation: HRESULT 0x%08x", uint32(hr))
			}
			return result, nil
		case 2:
			return nil, fmt.Errorf("Windows Runtime operation was canceled")
		default:
			var operationHR uint32
			_, _, _ = syscall.SyscallN(info.vtable[8], uintptr(unsafe.Pointer(info)), uintptr(unsafe.Pointer(&operationHR)))
			return nil, fmt.Errorf("Windows Runtime operation failed: HRESULT 0x%08x", operationHR)
		}
	}
}

func hstring(value uintptr) string {
	var length uint32
	buffer, _, _ := windowsGetStringBuffer.Call(value, uintptr(unsafe.Pointer(&length)))
	if buffer == 0 || length == 0 {
		return ""
	}
	units := make([]uint16, length)
	_, _, _ = rtlMoveMemory.Call(uintptr(unsafe.Pointer(&units[0])), buffer, uintptr(length)*2)
	return string(utf16.Decode(units))
}

// Extract recognizes text in the first frame of an image through the Windows 10+ inbox OCR engine.
func Extract(path string) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := roInitialize.Call(1) // RO_INIT_MULTITHREADED
	if failed(hr) {
		return "", fmt.Errorf("initialize Windows Runtime: HRESULT 0x%08x", uint32(hr))
	}
	defer roUninitialize.Call()

	engineFactory, err := activationFactory("Windows.Media.Ocr.OcrEngine", &iidOcrEngineStatics)
	if err != nil {
		return "", err
	}
	defer release(engineFactory)
	var engine *inspectable
	hr, _, _ = syscall.SyscallN(engineFactory.vtable[10], uintptr(unsafe.Pointer(engineFactory)), uintptr(unsafe.Pointer(&engine)))
	if failed(hr) {
		return "", fmt.Errorf("create OCR engine for user languages: HRESULT 0x%08x", uint32(hr))
	}
	if engine == nil {
		return "", fmt.Errorf("no Windows OCR language matches the user profile")
	}
	defer release(engine)

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	wide, err := syscall.UTF16PtrFromString(absolute)
	if err != nil {
		return "", err
	}
	var stream *inspectable
	hr, _, _ = createRandomAccessOnFile.Call(uintptr(unsafe.Pointer(wide)), 0, uintptr(unsafe.Pointer(&iidRandomAccessStream)), uintptr(unsafe.Pointer(&stream)))
	if failed(hr) {
		return "", fmt.Errorf("open image for OCR: HRESULT 0x%08x", uint32(hr))
	}
	defer release(stream)

	decoderFactory, err := activationFactory("Windows.Graphics.Imaging.BitmapDecoder", &iidBitmapDecoderStatics)
	if err != nil {
		return "", err
	}
	defer release(decoderFactory)
	var operation *inspectable
	hr, _, _ = syscall.SyscallN(decoderFactory.vtable[14], uintptr(unsafe.Pointer(decoderFactory)), uintptr(unsafe.Pointer(stream)), uintptr(unsafe.Pointer(&operation)))
	if failed(hr) {
		return "", fmt.Errorf("decode image for OCR: HRESULT 0x%08x", uint32(hr))
	}
	defer release(operation)
	decoder, err := asyncResult(operation)
	if err != nil {
		return "", err
	}
	defer release(decoder)

	frame, err := query(decoder, &iidBitmapFrameSoftware)
	if err != nil {
		return "", err
	}
	defer release(frame)
	var bitmapOperation *inspectable
	hr, _, _ = syscall.SyscallN(frame.vtable[6], uintptr(unsafe.Pointer(frame)), uintptr(unsafe.Pointer(&bitmapOperation)))
	if failed(hr) {
		return "", fmt.Errorf("convert image for OCR: HRESULT 0x%08x", uint32(hr))
	}
	defer release(bitmapOperation)
	bitmap, err := asyncResult(bitmapOperation)
	if err != nil {
		return "", err
	}
	defer release(bitmap)

	var ocrOperation *inspectable
	hr, _, _ = syscall.SyscallN(engine.vtable[6], uintptr(unsafe.Pointer(engine)), uintptr(unsafe.Pointer(bitmap)), uintptr(unsafe.Pointer(&ocrOperation)))
	if failed(hr) {
		return "", fmt.Errorf("recognize image text: HRESULT 0x%08x", uint32(hr))
	}
	defer release(ocrOperation)
	result, err := asyncResult(ocrOperation)
	if err != nil {
		return "", err
	}
	defer release(result)
	var textValue uintptr
	hr, _, _ = syscall.SyscallN(result.vtable[8], uintptr(unsafe.Pointer(result)), uintptr(unsafe.Pointer(&textValue)))
	if failed(hr) {
		return "", fmt.Errorf("read OCR text: HRESULT 0x%08x", uint32(hr))
	}
	defer windowsDeleteString.Call(textValue)
	text := strings.TrimSpace(hstring(textValue))
	if text == "" {
		return "", ErrNoText
	}
	return text, nil
}
