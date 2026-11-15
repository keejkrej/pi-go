//go:build linux

// Ported from packages/tui/native/linux/src/linux-platform-x11.c and clipboard-worker.h (pi v1.0.0).

package tui

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Linux N-API exports are getText and getImage. clipboard-worker.h runs each read on a
// private thread; a busy or unready worker resolves unavailable. There is no setText export.

type npLinuxHelper struct{}

var _ NativeClipboard = (*npLinuxHelper)(nil)

func npPlatformNewHelper(platform, suffix string) NativeClipboard {
	if platform != "linux" || suffix != "-x11" {
		return nil
	}
	return &npLinuxHelper{}
}

func (h *npLinuxHelper) GetText() (*string, bool, error) {
	return npGetText()
}

func (h *npLinuxHelper) GetImage() ([]byte, bool, error) {
	return npGetImage()
}

const (
	npX11ClipboardTimeout = 2000 * time.Millisecond
	npX11MaxClipboardSize = 50 * 1024 * 1024
)

var (
	npLinuxWorkerMu      sync.Mutex
	npX11TextTargetNames = []string{
		"text/plain;charset=utf-8",
		"text/plain;charset=UTF-8",
		"UTF8_STRING",
		"text/plain",
		"STRING",
	}
	npX11ImageTargetNames = []string{
		"image/png",
		"image/jpeg",
		"image/webp",
		"image/gif",
		"image/bmp",
		"image/tiff",
	}
)

type npX11EventOrErr struct {
	ev   xgb.Event
	xerr xgb.Error
}

func npX11Intern(c *xgb.Conn, name string) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(c, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return reply.Atom, nil
}

func npX11Latin1ToUTF8(raw []byte) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, ch := range raw {
		b.WriteRune(rune(ch))
	}
	return b.String()
}

func npX11WaitEvent(evCh <-chan npX11EventOrErr, deadline time.Time, match func(xgb.Event) bool) (xgb.Event, bool) {
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, false
		}
		timer := time.NewTimer(remaining)
		select {
		case item, ok := <-evCh:
			timer.Stop()
			if !ok {
				return nil, false
			}
			if item.ev != nil && match(item.ev) {
				return item.ev, true
			}
		case <-timer.C:
			return nil, false
		}
	}
}

// npX11ReadClipboard returns (data, propType, available, emptyPayload, err).
// available=false means DISPLAY/X11 connection/worker unavailable; emptyPayload=true means reachable & empty.
func npX11ReadClipboard(wantImage bool) ([]byte, xproto.Atom, bool, bool, error) {
	if os.Getenv("DISPLAY") == "" {
		return nil, 0, false, false, nil
	}
	if !npLinuxWorkerMu.TryLock() {
		return nil, 0, false, false, nil
	}
	defer npLinuxWorkerMu.Unlock()

	c, err := xgb.NewConn()
	if err != nil {
		return nil, 0, false, false, nil
	}
	closed := false
	closeConn := func() {
		if !closed {
			closed = true
			c.Close()
		}
	}
	defer closeConn()

	atomClipboard, err := npX11Intern(c, "CLIPBOARD")
	if err != nil {
		return nil, 0, false, false, err
	}
	atomProp, err := npX11Intern(c, "PI_CLIPBOARD")
	if err != nil {
		return nil, 0, false, false, err
	}
	atomTargets, err := npX11Intern(c, "TARGETS")
	if err != nil {
		return nil, 0, false, false, err
	}
	atomIncr, err := npX11Intern(c, "INCR")
	if err != nil {
		return nil, 0, false, false, err
	}
	atomUTF8String, err := npX11Intern(c, "UTF8_STRING")
	if err != nil {
		return nil, 0, false, false, err
	}

	targetNames := npX11TextTargetNames
	if wantImage {
		targetNames = npX11ImageTargetNames
	}
	targetAtoms := make([]xproto.Atom, len(targetNames))
	for i, name := range targetNames {
		atom, err := npX11Intern(c, name)
		if err != nil {
			return nil, 0, false, false, err
		}
		targetAtoms[i] = atom
	}

	ownerReply, err := xproto.GetSelectionOwner(c, atomClipboard).Reply()
	if err != nil {
		return nil, 0, false, false, err
	}
	if ownerReply == nil || ownerReply.Owner == xproto.WindowNone {
		return nil, 0, true, true, nil
	}

	screen := xproto.Setup(c).DefaultScreen(c)
	win, err := xproto.NewWindowId(c)
	if err != nil {
		return nil, 0, false, false, err
	}
	xproto.CreateWindow(
		c,
		0,
		win,
		screen.Root,
		0, 0, 1, 1, 0,
		xproto.WindowClassInputOnly,
		0,
		xproto.CwEventMask,
		[]uint32{xproto.EventMaskPropertyChange},
	)
	defer func() {
		if !closed {
			xproto.DestroyWindow(c, win)
		}
	}()

	stopCh := make(chan struct{})
	defer close(stopCh)
	evCh := make(chan npX11EventOrErr, 16)
	go func() {
		defer close(evCh)
		for {
			ev, xerr := c.WaitForEvent()
			if ev == nil && xerr == nil {
				return
			}
			select {
			case evCh <- npX11EventOrErr{ev: ev, xerr: xerr}:
			case <-stopCh:
				return
			}
		}
	}()

	deadline := time.Now().Add(npX11ClipboardTimeout)

	// Step 1: Query supported TARGETS.
	xproto.ConvertSelection(c, win, atomClipboard, atomTargets, atomProp, xproto.TimeCurrentTime)
	ev, ok := npX11WaitEvent(evCh, deadline, func(e xgb.Event) bool {
		sn, ok := e.(xproto.SelectionNotifyEvent)
		return ok && sn.Requestor == win && sn.Selection == atomClipboard && sn.Target == atomTargets
	})
	if !ok {
		return nil, 0, false, false, nil
	}
	sn := ev.(xproto.SelectionNotifyEvent)

	var chosenTarget xproto.Atom
	if sn.Property == xproto.AtomNone {
		if wantImage {
			return nil, 0, true, true, nil
		}
		chosenTarget = atomUTF8String
	} else {
		targetsReply, err := xproto.GetProperty(c, true, win, atomProp, xproto.AtomAtom, 0, 1024).Reply()
		if err != nil || targetsReply == nil || targetsReply.Type == atomIncr || targetsReply.Format != 32 {
			return nil, 0, true, true, nil
		}
		count := len(targetsReply.Value) / 4
		offered := make(map[xproto.Atom]struct{}, count)
		for i := 0; i < count; i++ {
			offered[xproto.Atom(xgb.Get32(targetsReply.Value[i*4:]))] = struct{}{}
		}
		for _, cand := range targetAtoms {
			if _, found := offered[cand]; found {
				chosenTarget = cand
				break
			}
		}
		if chosenTarget == 0 {
			return nil, 0, true, true, nil
		}
	}

	// Step 2: Request chosen target.
	xproto.DeleteProperty(c, win, atomProp)
	xproto.ConvertSelection(c, win, atomClipboard, chosenTarget, atomProp, xproto.TimeCurrentTime)
	ev, ok = npX11WaitEvent(evCh, deadline, func(e xgb.Event) bool {
		sn2, ok := e.(xproto.SelectionNotifyEvent)
		return ok && sn2.Requestor == win && sn2.Selection == atomClipboard && sn2.Target == chosenTarget
	})
	if !ok {
		return nil, 0, false, false, nil
	}
	sn2 := ev.(xproto.SelectionNotifyEvent)
	if sn2.Property == xproto.AtomNone {
		return nil, 0, true, true, nil
	}

	propReply, err := xproto.GetProperty(c, true, win, atomProp, xproto.GetPropertyTypeAny, 0, npX11MaxClipboardSize/4).Reply()
	if err != nil || propReply == nil {
		return nil, 0, true, true, nil
	}

	if propReply.Type != atomIncr {
		if len(propReply.Value) > npX11MaxClipboardSize {
			return nil, 0, true, true, nil
		}
		return propReply.Value, propReply.Type, true, false, nil
	}

	// Handle INCR incremental transfer.
	var data []byte
	var dataType xproto.Atom
	for {
		_, ok := npX11WaitEvent(evCh, deadline, func(e xgb.Event) bool {
			pn, ok := e.(xproto.PropertyNotifyEvent)
			return ok && pn.Window == win && pn.Atom == atomProp && pn.State == xproto.PropertyNewValue
		})
		if !ok {
			return nil, 0, false, false, nil
		}
		chunk, err := xproto.GetProperty(c, true, win, atomProp, xproto.GetPropertyTypeAny, 0, npX11MaxClipboardSize/4).Reply()
		if err != nil || chunk == nil {
			return nil, 0, true, true, nil
		}
		if len(chunk.Value) == 0 {
			break
		}
		if dataType == 0 {
			dataType = chunk.Type
		}
		if len(data)+len(chunk.Value) > npX11MaxClipboardSize {
			return nil, 0, true, true, nil
		}
		data = append(data, chunk.Value...)
	}
	return data, dataType, true, false, nil
}

// npGetText is the getText export.
// ok false is unavailable. A nil text with ok true is an empty clipboard. A non-nil error rejects.
func npGetText() (text *string, ok bool, err error) {
	data, dataType, avail, empty, err := npX11ReadClipboard(false)
	if err != nil || !avail {
		return nil, false, err
	}
	if empty {
		return nil, true, nil
	}
	var s string
	if dataType == xproto.AtomString {
		s = npX11Latin1ToUTF8(data)
	} else {
		s = string(data)
	}
	return &s, true, nil
}

// npGetImage is the getImage export.
// ok false is unavailable. A nil image with ok true means no image.
func npGetImage() (image []byte, ok bool, err error) {
	data, _, avail, empty, err := npX11ReadClipboard(true)
	if err != nil || !avail {
		return nil, false, err
	}
	if empty || len(data) == 0 {
		return nil, true, nil
	}
	return data, true, nil
}
