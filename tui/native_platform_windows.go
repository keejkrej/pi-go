//go:build windows

// Ported from packages/tui/native/win32/src/win32-platform.c (pi v1.0.0).

package tui

import (
	"encoding/binary"
	"errors"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type npWindowsHelper struct{}

var (
	_ NativeClipboard        = (*npWindowsHelper)(nil)
	_ NativeClipboardSetText = (*npWindowsHelper)(nil)
	_ npNativePlatformHelper = (*npWindowsHelper)(nil)
	_ npVirtualTerminalInput = (*npWindowsHelper)(nil)
)

func npPlatformNewHelper(platform, suffix string) NativeClipboard {
	if (platform != "win32" && platform != "windows") || suffix != "" {
		return nil
	}
	return &npWindowsHelper{}
}

func (h *npWindowsHelper) GetText() (*string, bool, error) {
	return npGetText()
}

func (h *npWindowsHelper) SetText(text string) error {
	return npSetText(text)
}

func (h *npWindowsHelper) GetImage() ([]byte, bool, error) {
	return npGetImage()
}

func (h *npWindowsHelper) IsModifierPressed(key ModifierKey) (bool, error) {
	return npIsModifierPressed(string(key))
}

func (h *npWindowsHelper) EnableVirtualTerminalInput() (bool, error) {
	return npEnableVirtualTerminalInput()
}

const (
	npWinCFUnicodeText    uintptr = 13
	npWinCFDIB            uintptr = 8
	npWinCFDIBV5          uintptr = 17
	npWinGmemMoveable     uintptr = 0x0002
	npWinHWNDMessage      uintptr = ^uintptr(2) // (HWND)-3
	npWinBitmapFileHdrLen         = 14
	npWinClipboardRetries         = 10
	npWinClipboardDelay           = 5 * time.Millisecond

	npWinVKShift    uintptr = 0x10
	npWinVKControl  uintptr = 0x11
	npWinVKMenu     uintptr = 0x12
	npWinVKLWin     uintptr = 0x5B
	npWinVKRWin     uintptr = 0x5C
	npWinVKLShift   uintptr = 0xA0
	npWinVKRShift   uintptr = 0xA1
	npWinVKLControl uintptr = 0xA2
	npWinVKRControl uintptr = 0xA3
	npWinVKLMenu    uintptr = 0xA4
	npWinVKRMenu    uintptr = 0xA5
)

var (
	npWinUser32                       = windows.NewLazySystemDLL("user32.dll")
	npWinKernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	npWinProcGetAsyncKeyState         = npWinUser32.NewProc("GetAsyncKeyState")
	npWinProcOpenClipboard            = npWinUser32.NewProc("OpenClipboard")
	npWinProcCloseClipboard           = npWinUser32.NewProc("CloseClipboard")
	npWinProcEmptyClipboard           = npWinUser32.NewProc("EmptyClipboard")
	npWinProcGetClipboardData         = npWinUser32.NewProc("GetClipboardData")
	npWinProcSetClipboardData         = npWinUser32.NewProc("SetClipboardData")
	npWinProcIsClipboardFormatAvail   = npWinUser32.NewProc("IsClipboardFormatAvailable")
	npWinProcRegisterClipboardFormatW = npWinUser32.NewProc("RegisterClipboardFormatW")
	npWinProcCreateWindowExW          = npWinUser32.NewProc("CreateWindowExW")
	npWinProcDestroyWindow            = npWinUser32.NewProc("DestroyWindow")
	npWinProcGlobalAlloc              = npWinKernel32.NewProc("GlobalAlloc")
	npWinProcGlobalFree               = npWinKernel32.NewProc("GlobalFree")
	npWinProcGlobalLock               = npWinKernel32.NewProc("GlobalLock")
	npWinProcGlobalUnlock             = npWinKernel32.NewProc("GlobalUnlock")
	npWinProcGlobalSize               = npWinKernel32.NewProc("GlobalSize")
)

func npWinPtr(addr uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}

func npWinOpenClipboardWithRetry(hwnd uintptr) bool {
	for i := 0; i < npWinClipboardRetries; i++ {
		r, _, _ := npWinProcOpenClipboard.Call(hwnd)
		if r != 0 {
			return true
		}
		time.Sleep(npWinClipboardDelay)
	}
	return false
}

func npWinDIBPixelOffset(dib []byte) (uint32, bool) {
	if len(dib) < 12 {
		return 0, false
	}
	biSize := binary.LittleEndian.Uint32(dib[0:4])
	if biSize < 12 || int(biSize) > len(dib) {
		return 0, false
	}

	var bitCount uint16
	var compression uint32
	var clrUsed uint32
	if biSize == 12 {
		bitCount = binary.LittleEndian.Uint16(dib[10:12])
		if bitCount <= 8 {
			clrUsed = 1 << bitCount
		}
		return biSize + clrUsed*3, true
	}

	if biSize < 40 || len(dib) < 40 {
		return 0, false
	}
	bitCount = binary.LittleEndian.Uint16(dib[14:16])
	compression = binary.LittleEndian.Uint32(dib[16:20])
	clrUsed = binary.LittleEndian.Uint32(dib[32:36])

	paletteEntries := clrUsed
	if paletteEntries == 0 && bitCount <= 8 {
		paletteEntries = 1 << bitCount
	}

	var maskBytes uint32
	if biSize == 40 {
		switch compression {
		case 3: // BI_BITFIELDS
			maskBytes = 12
		case 6: // BI_ALPHABITFIELDS
			maskBytes = 16
		}
	}

	return biSize + maskBytes + paletteEntries*4, true
}

func npWinReadClipboardData(format uintptr) ([]byte, bool) {
	hData, _, _ := npWinProcGetClipboardData.Call(format)
	if hData == 0 {
		return nil, false
	}
	size, _, _ := npWinProcGlobalSize.Call(hData)
	if size == 0 {
		return nil, false
	}
	ptr, _, _ := npWinProcGlobalLock.Call(hData)
	if ptr == 0 {
		return nil, false
	}
	defer npWinProcGlobalUnlock.Call(hData)

	src := unsafe.Slice((*byte)(npWinPtr(ptr)), int(size))
	out := make([]byte, len(src))
	copy(out, src)
	return out, true
}

// npEnableVirtualTerminalInput is the enableVirtualTerminalInput export.
// It sets ENABLE_VIRTUAL_TERMINAL_INPUT on stdin. A non-nil error rejects.
func npEnableVirtualTerminalInput() (enabled bool, err error) {
	h, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil || h == 0 || h == windows.InvalidHandle {
		return false, nil
	}
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false, nil
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_INPUT != 0 {
		return true, nil
	}
	if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT); err != nil {
		return false, err
	}
	return true, nil
}

// npIsModifierPressed is the isModifierPressed export.
// shift, control, option/alt, and command/super/win are recognized. An unknown name is false.
func npIsModifierPressed(name string) (pressed bool, err error) {
	down := func(vk uintptr) bool {
		r, _, _ := npWinProcGetAsyncKeyState.Call(vk)
		return (uint16(r) & 0x8000) != 0
	}
	switch name {
	case string(ModifierKeyShift):
		return down(npWinVKShift) || down(npWinVKLShift) || down(npWinVKRShift), nil
	case string(ModifierKeyControl):
		return down(npWinVKControl) || down(npWinVKLControl) || down(npWinVKRControl), nil
	case string(ModifierKeyOption), "alt":
		return down(npWinVKMenu) || down(npWinVKLMenu) || down(npWinVKRMenu), nil
	case string(ModifierKeyCommand), "super", "win":
		return down(npWinVKLWin) || down(npWinVKRWin), nil
	default:
		return false, nil
	}
}

// npGetText is the getText export.
// ok false is unavailable. A nil text with ok true is an empty clipboard. A non-nil error rejects.
func npGetText() (text *string, ok bool, err error) {
	avail, _, _ := npWinProcIsClipboardFormatAvail.Call(npWinCFUnicodeText)
	if avail == 0 {
		return nil, true, nil
	}
	if !npWinOpenClipboardWithRetry(0) {
		return nil, false, errors.New("Failed to open clipboard")
	}
	defer npWinProcCloseClipboard.Call()

	hData, _, _ := npWinProcGetClipboardData.Call(npWinCFUnicodeText)
	if hData == 0 {
		return nil, true, nil
	}
	size, _, _ := npWinProcGlobalSize.Call(hData)
	ptr, _, _ := npWinProcGlobalLock.Call(hData)
	if ptr == 0 {
		return nil, false, nil
	}
	defer npWinProcGlobalUnlock.Call(hData)

	maxUnits := int(size) / 2
	if maxUnits == 0 {
		empty := ""
		return &empty, true, nil
	}
	src := unsafe.Slice((*uint16)(npWinPtr(ptr)), maxUnits)
	s := windows.UTF16ToString(src)
	return &s, true, nil
}

// npSetText is the setText export.
func npSetText(text string) error {
	var owner uintptr
	classPtr, _ := windows.UTF16PtrFromString("STATIC")
	titlePtr, _ := windows.UTF16PtrFromString("")
	if classPtr != nil && titlePtr != nil {
		hwnd, _, _ := npWinProcCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(classPtr)),
			uintptr(unsafe.Pointer(titlePtr)),
			0, 0, 0, 0, 0,
			npWinHWNDMessage,
			0, 0, 0,
		)
		owner = hwnd
		if owner != 0 {
			defer npWinProcDestroyWindow.Call(owner)
		}
	}

	units, err := windows.UTF16FromString(text)
	if err != nil {
		units = append(utf16.Encode([]rune(text)), 0)
	}
	byteLen := uintptr(len(units) * 2)
	hMem, _, _ := npWinProcGlobalAlloc.Call(npWinGmemMoveable, byteLen)
	if hMem == 0 {
		return errors.New("Failed to allocate clipboard buffer")
	}
	ptr, _, _ := npWinProcGlobalLock.Call(hMem)
	if ptr == 0 {
		npWinProcGlobalFree.Call(hMem)
		return errors.New("Failed to lock clipboard buffer")
	}
	dst := unsafe.Slice((*uint16)(npWinPtr(ptr)), len(units))
	copy(dst, units)
	npWinProcGlobalUnlock.Call(hMem)

	if !npWinOpenClipboardWithRetry(owner) {
		npWinProcGlobalFree.Call(hMem)
		return errors.New("Failed to open clipboard")
	}
	npWinProcEmptyClipboard.Call()
	res, _, _ := npWinProcSetClipboardData.Call(npWinCFUnicodeText, hMem)
	npWinProcCloseClipboard.Call()
	if res == 0 {
		npWinProcGlobalFree.Call(hMem)
		return errors.New("Failed to write text to clipboard")
	}
	return nil
}

// npGetImage is the getImage export.
// ok false is unavailable. A nil image with ok true means no image.
func npGetImage() (image []byte, ok bool, err error) {
	pngNamePtr, _ := windows.UTF16PtrFromString("PNG")
	var pngFmt uintptr
	if pngNamePtr != nil {
		pngFmt, _, _ = npWinProcRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(pngNamePtr)))
	}
	hasPNG := false
	if pngFmt != 0 {
		r, _, _ := npWinProcIsClipboardFormatAvail.Call(pngFmt)
		hasPNG = r != 0
	}
	rV5, _, _ := npWinProcIsClipboardFormatAvail.Call(npWinCFDIBV5)
	rDIB, _, _ := npWinProcIsClipboardFormatAvail.Call(npWinCFDIB)
	hasDIBV5 := rV5 != 0
	hasDIB := rDIB != 0
	if !hasPNG && !hasDIBV5 && !hasDIB {
		return nil, true, nil
	}

	if !npWinOpenClipboardWithRetry(0) {
		return nil, false, errors.New("Failed to open clipboard")
	}
	defer npWinProcCloseClipboard.Call()

	if hasPNG {
		if data, ok := npWinReadClipboardData(pngFmt); ok && len(data) > 0 {
			return data, true, nil
		}
	}

	dibFormat := npWinCFDIB
	if hasDIBV5 {
		dibFormat = npWinCFDIBV5
	}
	dib, ok := npWinReadClipboardData(dibFormat)
	if !ok || len(dib) == 0 {
		return nil, true, nil
	}
	pixelOffset, valid := npWinDIBPixelOffset(dib)
	if !valid {
		return nil, true, nil
	}

	bmp := make([]byte, npWinBitmapFileHdrLen+len(dib))
	bmp[0] = 'B'
	bmp[1] = 'M'
	binary.LittleEndian.PutUint32(bmp[2:6], uint32(len(bmp)))
	binary.LittleEndian.PutUint32(bmp[6:10], 0)
	binary.LittleEndian.PutUint32(bmp[10:14], npWinBitmapFileHdrLen+pixelOffset)
	copy(bmp[npWinBitmapFileHdrLen:], dib)
	return bmp, true, nil
}
