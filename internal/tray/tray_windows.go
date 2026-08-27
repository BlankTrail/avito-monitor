// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

// Package tray puts an icon in the Windows notification area.
//
// Written against golang.org/x/sys/windows directly, which is what the spec's
// dependency list budgets for — «the tray and the autostart entry» — and what
// this project does everywhere else: its own chart against image/png, its own
// Bot API client against net/http, eighty lines of JavaScript instead of htmx.
// A tray library would be a dependency for four calls.
//
// What it is for: a person who ticked «запускать при входе» has a monitor
// running with no window anywhere. Without an icon the only evidence it is
// alive is opening a browser and guessing the port, and the only way to stop it
// is Task Manager.
package tray

import (
	"context"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassEx  = user32.NewProc("RegisterClassExW")
	procCreateWindowEx   = user32.NewProc("CreateWindowExW")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procGetMessage       = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procPostMessage      = user32.NewProc("PostMessageW")
	procLoadIcon         = user32.NewProc("LoadIconW")
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procAppendMenu       = user32.NewProc("AppendMenuW")
	procDestroyMenu      = user32.NewProc("DestroyMenu")
	procTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procSetForegroundWin = user32.NewProc("SetForegroundWindow")

	procShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy = 0x0002
	wmClose   = 0x0010
	wmCommand = 0x0111
	wmApp     = 0x8000

	// wmTrayEvent is where the shell sends clicks on our icon. Any value above
	// WM_APP is ours to choose.
	wmTrayEvent = wmApp + 1

	wmRightButtonUp = 0x0205
	wmLeftDouble    = 0x0203

	nimAdd    = 0x0000
	nimDelete = 0x0002

	nifMessage = 0x0001
	nifIcon    = 0x0002
	nifTip     = 0x0004

	idiApplication = 32512

	// errClassAlreadyExists is what RegisterClassExW answers for a name this
	// process has already registered.
	errClassAlreadyExists = 1410

	mfString    = 0x0000
	mfSeparator = 0x0800

	tpmRightButton = 0x0002
	tpmLeftAlign   = 0x0000

	// The two menu items, and the two things a person can want from an icon
	// that represents a background process: see it, or stop it.
	cmdOpen = 1
	cmdQuit = 2
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             windows.HWND
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

type point struct{ X, Y int32 }

type msg struct {
	HWnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	private uint32
}

// Config is what the icon does.
type Config struct {
	// Tip is the hover text. Truncated to 127 runes by Windows itself; kept
	// short here because a tooltip is read at a glance.
	Tip string
	// OpenLabel and QuitLabel are the menu. Russian, like everything a person
	// reads in this product.
	OpenLabel string
	QuitLabel string
	// OnOpen is what a double-click and «Открыть панель» do.
	OnOpen func()
	// OnQuit is what «Выход» does. Run before the message loop ends, so a
	// caller can cancel its context and let the daemon stop cleanly.
	OnQuit func()
}

// Run puts the icon up and pumps messages until the context is cancelled or
// somebody chooses Выход.
//
// It must be called from a goroutine locked to an OS thread: a window belongs
// to the thread that created it and only that thread may pump its messages.
// Run does the locking itself rather than documenting the requirement, because
// a requirement in a comment is one a caller forgets once.
func Run(ctx context.Context, cfg Config) error {
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()
	return <-done
}

func run(ctx context.Context, cfg Config) (err error) {
	// The whole of this function runs on one thread, for the life of the
	// window. Unlocking would let the runtime move the message loop to another
	// thread, where GetMessage would return nothing for ever.
	lockOSThread()
	defer unlockOSThread()

	instance, _, _ := procGetModuleHandle.Call(0)

	className, err := windows.UTF16PtrFromString("AvitoMonitorTray")
	if err != nil {
		return err
	}
	var window windows.HWND
	proc := syscall.NewCallback(func(hwnd windows.HWND, message uint32,
		wparam, lparam uintptr) uintptr {
		switch message {
		case wmTrayEvent:
			// The shell packs the mouse event into the low word of lParam.
			switch uint32(lparam) {
			case wmRightButtonUp:
				showMenu(hwnd, cfg)
			case wmLeftDouble:
				if cfg.OnOpen != nil {
					cfg.OnOpen()
				}
			}
			return 0
		case wmCommand:
			switch wparam & 0xffff {
			case cmdOpen:
				if cfg.OnOpen != nil {
					cfg.OnOpen()
				}
			case cmdQuit:
				procPostMessage.Call(uintptr(hwnd), wmClose, 0, 0)
			}
			return 0
		case wmClose:
			procDestroyWindow.Call(uintptr(hwnd))
			return 0
		case wmDestroy:
			procPostQuitMessage.Call(0)
			return 0
		}
		r, _, _ := procDefWindowProc.Call(uintptr(hwnd), uintptr(message), wparam, lparam)
		return r
	})

	class := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   proc,
		HInstance:     windows.Handle(instance),
		LpszClassName: className,
	}
	// A class already registered is not a failure. The name is per-process, so
	// a second Run in one process — a restart of the icon without a restart of
	// the program, which is what a test does and what a future «перезапустить
	// значок» would do — hits this and must carry on. Found by the second test
	// in this package's own file failing where the first passed.
	if atom, _, e := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		if !errorsIs(e, errClassAlreadyExists) {
			return fmt.Errorf("tray: RegisterClassExW: %w", e)
		}
	}

	// A message-only window: it has no size, no position and never appears.
	// The icon needs a window to send its events to and nothing more.
	handle, _, e := procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0, 0, 0, instance, 0)
	if handle == 0 {
		return fmt.Errorf("tray: CreateWindowExW: %w", e)
	}
	window = windows.HWND(handle)

	icon, _, _ := procLoadIcon.Call(0, idiApplication)

	data := notifyIconData{
		CbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:             window,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: wmTrayEvent,
		HIcon:            windows.Handle(icon),
	}
	copyTip(&data.SzTip, cfg.Tip)

	if ok, _, e := procShellNotifyIcon.Call(nimAdd,
		uintptr(unsafe.Pointer(&data))); ok == 0 {
		procDestroyWindow.Call(uintptr(window))
		return fmt.Errorf("tray: Shell_NotifyIconW(NIM_ADD): %w", e)
	}
	// Removed however this returns. An icon left behind by a process that has
	// gone is the thing everybody has seen and nobody can click away.
	defer procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))

	// Cancelling the context has to reach the message loop, and the loop is
	// blocked inside GetMessage. Posting to the window is what wakes it.
	go func() {
		<-ctx.Done()
		procPostMessage.Call(uintptr(window), wmClose, 0, 0)
	}()

	var m msg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		// GetMessage returns 0 on WM_QUIT and -1 on error; anything else is a
		// message. The -1 case is why this is not a bool.
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	if cfg.OnQuit != nil {
		cfg.OnQuit()
	}
	return nil
}

// errorsIs compares a syscall error against a Windows error number.
//
// syscall.Errno rather than errors.Is, because these come back from Call as a
// raw error whose value IS the number and whose Is is not defined for one.
func errorsIs(err error, code uintptr) bool {
	e, ok := err.(syscall.Errno)
	return ok && uintptr(e) == code
}

func showMenu(hwnd windows.HWND, cfg Config) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendItem(menu, cmdOpen, cfg.OpenLabel)
	procAppendMenu.Call(menu, mfSeparator, 0, 0)
	appendItem(menu, cmdQuit, cfg.QuitLabel)

	// Without this the menu does not close when somebody clicks elsewhere —
	// a documented Win32 quirk and the single most common tray bug.
	procSetForegroundWin.Call(uintptr(hwnd))

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procTrackPopupMenu.Call(menu, tpmRightButton|tpmLeftAlign,
		uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)
}

func appendItem(menu uintptr, id uintptr, label string) {
	text, err := windows.UTF16PtrFromString(label)
	if err != nil {
		return
	}
	procAppendMenu.Call(menu, mfString, id, uintptr(unsafe.Pointer(text)))
}

func copyTip(dst *[128]uint16, s string) {
	encoded, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}
	// One short of the array, so the terminating NUL always fits: a tip
	// without one is however many bytes of adjacent struct Windows decides to
	// render.
	if len(encoded) > len(dst)-1 {
		encoded = encoded[:len(dst)-1]
		encoded[len(encoded)-1] = 0
	}
	copy(dst[:], encoded)
}
