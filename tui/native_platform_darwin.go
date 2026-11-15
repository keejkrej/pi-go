//go:build darwin

// Ported from packages/tui/native/darwin/src/darwin-platform.m (pi v1.0.0).

package tui

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type npDarwinHelper struct{}

var (
	_ NativeClipboard          = (*npDarwinHelper)(nil)
	_ NativeClipboardSetText   = (*npDarwinHelper)(nil)
	_ NativeClipboardFilePaths = (*npDarwinHelper)(nil)
	_ npNativePlatformHelper   = (*npDarwinHelper)(nil)
)

func npPlatformNewHelper(platform, suffix string) NativeClipboard {
	if platform != "darwin" || suffix != "" {
		return nil
	}
	return &npDarwinHelper{}
}

func (h *npDarwinHelper) GetText() (*string, bool, error) {
	return npGetText()
}

func (h *npDarwinHelper) SetText(text string) error {
	return npSetText(text)
}

func (h *npDarwinHelper) GetImage() ([]byte, bool, error) {
	return npGetImage()
}

func (h *npDarwinHelper) GetFilePaths() ([]string, bool, error) {
	return npGetFilePaths()
}

func (h *npDarwinHelper) IsModifierPressed(key ModifierKey) (bool, error) {
	return npIsModifierPressed(string(key))
}

const (
	npDarwinCGEventSourceCombinedSession int32  = 0
	npDarwinCGEventFlagMaskShift         uint64 = 0x00020000
	npDarwinCGEventFlagMaskControl       uint64 = 0x00040000
	npDarwinCGEventFlagMaskAlternate     uint64 = 0x00080000
	npDarwinCGEventFlagMaskCommand       uint64 = 0x00100000
	npDarwinNSUTF8StringEncoding         uint64 = 4
	npDarwinNSBitmapImageFileTypePNG     uint64 = 4
)

var (
	npDarwinCGOnce           sync.Once
	npDarwinCGErr            error
	npDarwinFlagsState       func(stateID int32) uint64
	npDarwinAppKitOnce       sync.Once
	npDarwinAppKitErr        error
	npDarwinPoolPush         func() uintptr
	npDarwinPoolPop          func(uintptr)
	npDarwinClassPasteboard  objc.Class
	npDarwinClassNSString    objc.Class
	npDarwinClassBitmapRep   objc.Class
	npDarwinClassNSArray     objc.Class
	npDarwinClassNSDict      objc.Class
	npDarwinClassNSURL       objc.Class
	npDarwinClassNSNumber    objc.Class
	npDarwinTypeString       objc.ID
	npDarwinTypePNG          objc.ID
	npDarwinTypeTIFF         objc.ID
	npDarwinFileURLsOnlyKey  objc.ID
	npDarwinSelGeneralPB     objc.SEL
	npDarwinSelStringForType objc.SEL
	npDarwinSelUTF8String    objc.SEL
	npDarwinSelClearContents objc.SEL
	npDarwinSelSetStringType objc.SEL
	npDarwinSelAlloc         objc.SEL
	npDarwinSelInitBytesEnc  objc.SEL
	npDarwinSelAutorelease   objc.SEL
	npDarwinSelStrWithUTF8   objc.SEL
	npDarwinSelDataForType   objc.SEL
	npDarwinSelLength        objc.SEL
	npDarwinSelGetBytesLen   objc.SEL
	npDarwinSelImageRepData  objc.SEL
	npDarwinSelRepUsingType  objc.SEL
	npDarwinSelArrayWithObj  objc.SEL
	npDarwinSelNumberBool    objc.SEL
	npDarwinSelDictWithObj   objc.SEL
	npDarwinSelReadObjects   objc.SEL
	npDarwinSelCount         objc.SEL
	npDarwinSelObjectAtIdx   objc.SEL
	npDarwinSelIsFileURL     objc.SEL
	npDarwinSelPath          objc.SEL
)

func npDarwinInitCG() error {
	npDarwinCGOnce.Do(func() {
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
		if err != nil {
			npDarwinCGErr = err
			return
		}
		purego.RegisterLibFunc(&npDarwinFlagsState, cg, "CGEventSourceFlagsState")
	})
	return npDarwinCGErr
}

func npDarwinDerefID(addr uintptr) objc.ID {
	if addr == 0 {
		return 0
	}
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
	return *(*objc.ID)(ptr)
}

func npDarwinNSStringLiteral(s string) objc.ID {
	return objc.ID(npDarwinClassNSString).Send(npDarwinSelStrWithUTF8, s+"\x00")
}

func npDarwinInitAppKit() error {
	npDarwinAppKitOnce.Do(func() {
		libObjc, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_LAZY)
		if err != nil {
			npDarwinAppKitErr = err
			return
		}
		purego.RegisterLibFunc(&npDarwinPoolPush, libObjc, "objc_autoreleasePoolPush")
		purego.RegisterLibFunc(&npDarwinPoolPop, libObjc, "objc_autoreleasePoolPop")

		if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_LAZY); err != nil {
			npDarwinAppKitErr = err
			return
		}
		appKit, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY)
		if err != nil {
			npDarwinAppKitErr = err
			return
		}

		npDarwinClassPasteboard = objc.GetClass("NSPasteboard")
		npDarwinClassNSString = objc.GetClass("NSString")
		npDarwinClassBitmapRep = objc.GetClass("NSBitmapImageRep")
		npDarwinClassNSArray = objc.GetClass("NSArray")
		npDarwinClassNSDict = objc.GetClass("NSDictionary")
		npDarwinClassNSURL = objc.GetClass("NSURL")
		npDarwinClassNSNumber = objc.GetClass("NSNumber")
		if npDarwinClassPasteboard == 0 || npDarwinClassNSString == 0 {
			npDarwinAppKitErr = errors.New("AppKit classes unavailable")
			return
		}

		npDarwinSelGeneralPB = objc.RegisterName("generalPasteboard")
		npDarwinSelStringForType = objc.RegisterName("stringForType:")
		npDarwinSelUTF8String = objc.RegisterName("UTF8String")
		npDarwinSelClearContents = objc.RegisterName("clearContents")
		npDarwinSelSetStringType = objc.RegisterName("setString:forType:")
		npDarwinSelAlloc = objc.RegisterName("alloc")
		npDarwinSelInitBytesEnc = objc.RegisterName("initWithBytes:length:encoding:")
		npDarwinSelAutorelease = objc.RegisterName("autorelease")
		npDarwinSelStrWithUTF8 = objc.RegisterName("stringWithUTF8String:")
		npDarwinSelDataForType = objc.RegisterName("dataForType:")
		npDarwinSelLength = objc.RegisterName("length")
		npDarwinSelGetBytesLen = objc.RegisterName("getBytes:length:")
		npDarwinSelImageRepData = objc.RegisterName("imageRepWithData:")
		npDarwinSelRepUsingType = objc.RegisterName("representationUsingType:properties:")
		npDarwinSelArrayWithObj = objc.RegisterName("arrayWithObject:")
		npDarwinSelNumberBool = objc.RegisterName("numberWithBool:")
		npDarwinSelDictWithObj = objc.RegisterName("dictionaryWithObject:forKey:")
		npDarwinSelReadObjects = objc.RegisterName("readObjectsForClasses:options:")
		npDarwinSelCount = objc.RegisterName("count")
		npDarwinSelObjectAtIdx = objc.RegisterName("objectAtIndex:")
		npDarwinSelIsFileURL = objc.RegisterName("isFileURL")
		npDarwinSelPath = objc.RegisterName("path")

		if sym, err := purego.Dlsym(appKit, "NSPasteboardTypeString"); err == nil {
			npDarwinTypeString = npDarwinDerefID(sym)
		}
		if npDarwinTypeString == 0 {
			npDarwinTypeString = npDarwinNSStringLiteral("public.utf8-plain-text")
		}
		if sym, err := purego.Dlsym(appKit, "NSPasteboardTypePNG"); err == nil {
			npDarwinTypePNG = npDarwinDerefID(sym)
		}
		if npDarwinTypePNG == 0 {
			npDarwinTypePNG = npDarwinNSStringLiteral("public.png")
		}
		if sym, err := purego.Dlsym(appKit, "NSPasteboardTypeTIFF"); err == nil {
			npDarwinTypeTIFF = npDarwinDerefID(sym)
		}
		if npDarwinTypeTIFF == 0 {
			npDarwinTypeTIFF = npDarwinNSStringLiteral("public.tiff")
		}
		if sym, err := purego.Dlsym(appKit, "NSPasteboardURLReadingFileURLsOnlyKey"); err == nil {
			npDarwinFileURLsOnlyKey = npDarwinDerefID(sym)
		}
		if npDarwinFileURLsOnlyKey == 0 {
			npDarwinFileURLsOnlyKey = npDarwinNSStringLiteral("NSPasteboardURLReadingFileURLsOnlyKey")
		}
	})
	return npDarwinAppKitErr
}

func npDarwinCopyNSData(data objc.ID) []byte {
	if data == 0 {
		return nil
	}
	n := int(objc.Send[uintptr](data, npDarwinSelLength))
	if n <= 0 {
		return nil
	}
	buf := make([]byte, n)
	data.Send(npDarwinSelGetBytesLen, &buf[0], uintptr(n))
	return buf
}

// npIsModifierPressed is the isModifierPressed export.
// Names are shift, command, control, and option. An unknown name is false.
func npIsModifierPressed(name string) (pressed bool, err error) {
	if err := npDarwinInitCG(); err != nil {
		return false, err
	}
	flags := npDarwinFlagsState(npDarwinCGEventSourceCombinedSession)
	switch name {
	case string(ModifierKeyShift):
		return (flags & npDarwinCGEventFlagMaskShift) != 0, nil
	case string(ModifierKeyControl):
		return (flags & npDarwinCGEventFlagMaskControl) != 0, nil
	case string(ModifierKeyOption):
		return (flags & npDarwinCGEventFlagMaskAlternate) != 0, nil
	case string(ModifierKeyCommand):
		return (flags & npDarwinCGEventFlagMaskCommand) != 0, nil
	default:
		return false, nil
	}
}

// npGetText is the getText export.
// ok false is unavailable. A nil text with ok true is an empty clipboard. A non-nil error rejects.
func npGetText() (text *string, ok bool, err error) {
	if err := npDarwinInitAppKit(); err != nil {
		return nil, false, err
	}
	pool := npDarwinPoolPush()
	defer npDarwinPoolPop(pool)

	pb := objc.ID(npDarwinClassPasteboard).Send(npDarwinSelGeneralPB)
	if pb == 0 {
		return nil, false, nil
	}
	strID := pb.Send(npDarwinSelStringForType, npDarwinTypeString)
	if strID == 0 {
		return nil, true, nil
	}
	s := objc.Send[string](strID, npDarwinSelUTF8String)
	return &s, true, nil
}

// npSetText is the setText export.
func npSetText(text string) error {
	if err := npDarwinInitAppKit(); err != nil {
		return err
	}
	pool := npDarwinPoolPush()
	defer npDarwinPoolPop(pool)

	var nsStr objc.ID
	if len(text) == 0 {
		nsStr = objc.ID(npDarwinClassNSString).Send(npDarwinSelStrWithUTF8, "\x00")
	} else {
		raw := []byte(text)
		alloc := objc.ID(npDarwinClassNSString).Send(npDarwinSelAlloc)
		nsStr = alloc.Send(npDarwinSelInitBytesEnc, &raw[0], uintptr(len(raw)), uintptr(npDarwinNSUTF8StringEncoding))
		if nsStr != 0 {
			nsStr = nsStr.Send(npDarwinSelAutorelease)
		}
	}
	if nsStr == 0 {
		return errors.New("Failed to convert text to NSString")
	}

	pb := objc.ID(npDarwinClassPasteboard).Send(npDarwinSelGeneralPB)
	if pb == 0 {
		return errors.New("generalPasteboard unavailable")
	}
	pb.Send(npDarwinSelClearContents)
	wrote := objc.Send[byte](pb, npDarwinSelSetStringType, nsStr, npDarwinTypeString)
	if wrote == 0 {
		return errors.New("Failed to write text to clipboard")
	}
	return nil
}

// npGetImage is the getImage export.
// ok false is unavailable. A nil image with ok true means no image. Bytes may be PNG converted from TIFF.
func npGetImage() (image []byte, ok bool, err error) {
	if err := npDarwinInitAppKit(); err != nil {
		return nil, false, err
	}
	pool := npDarwinPoolPush()
	defer npDarwinPoolPop(pool)

	pb := objc.ID(npDarwinClassPasteboard).Send(npDarwinSelGeneralPB)
	if pb == 0 {
		return nil, false, nil
	}

	pngData := pb.Send(npDarwinSelDataForType, npDarwinTypePNG)
	if bytes := npDarwinCopyNSData(pngData); len(bytes) > 0 {
		return bytes, true, nil
	}

	tiffData := pb.Send(npDarwinSelDataForType, npDarwinTypeTIFF)
	if tiffData != 0 && objc.Send[uintptr](tiffData, npDarwinSelLength) > 0 && npDarwinClassBitmapRep != 0 {
		rep := objc.ID(npDarwinClassBitmapRep).Send(npDarwinSelImageRepData, tiffData)
		if rep != 0 {
			converted := rep.Send(npDarwinSelRepUsingType, uintptr(npDarwinNSBitmapImageFileTypePNG), uintptr(0))
			if bytes := npDarwinCopyNSData(converted); len(bytes) > 0 {
				return bytes, true, nil
			}
		}
	}
	return nil, true, nil
}

// npGetFilePaths is the getFilePaths export.
// ok false is unavailable. A nil slice with ok true means no file URLs.
func npGetFilePaths() (paths []string, ok bool, err error) {
	if err := npDarwinInitAppKit(); err != nil {
		return nil, false, err
	}
	pool := npDarwinPoolPush()
	defer npDarwinPoolPop(pool)

	pb := objc.ID(npDarwinClassPasteboard).Send(npDarwinSelGeneralPB)
	if pb == 0 || npDarwinClassNSURL == 0 || npDarwinClassNSArray == 0 {
		return nil, false, nil
	}
	classArray := objc.ID(npDarwinClassNSArray).Send(npDarwinSelArrayWithObj, objc.ID(npDarwinClassNSURL))
	var options objc.ID
	if npDarwinClassNSDict != 0 && npDarwinClassNSNumber != 0 && npDarwinFileURLsOnlyKey != 0 {
		yes := objc.ID(npDarwinClassNSNumber).Send(npDarwinSelNumberBool, uintptr(1))
		options = objc.ID(npDarwinClassNSDict).Send(npDarwinSelDictWithObj, yes, npDarwinFileURLsOnlyKey)
	}
	urls := pb.Send(npDarwinSelReadObjects, classArray, options)
	if urls == 0 {
		return nil, true, nil
	}
	count := int(objc.Send[uintptr](urls, npDarwinSelCount))
	if count == 0 {
		return nil, true, nil
	}
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		u := urls.Send(npDarwinSelObjectAtIdx, uintptr(i))
		if u == 0 || objc.Send[byte](u, npDarwinSelIsFileURL) == 0 {
			continue
		}
		pathID := u.Send(npDarwinSelPath)
		if pathID == 0 {
			continue
		}
		s := objc.Send[string](pathID, npDarwinSelUTF8String)
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, true, nil
	}
	return out, true, nil
}
