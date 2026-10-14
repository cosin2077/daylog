//go:build windows

package native

import (
	"golang.org/x/sys/windows"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

var displayOff atomic.Bool
var powerOnce sync.Once
var powerReady = make(chan struct{})
var powerReadyOnce sync.Once
var powerWindow atomic.Uintptr

// One sleeping message thread receives display-power changes. No polling,
// hooks, keyboard recording, wake lock or system-wide power settings changes.
func startPowerObserver() {
	powerOnce.Do(func() {
		go func() {
			defer powerReadyOnce.Do(func() { close(powerReady) })
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			name, _ := windows.UTF16PtrFromString("DaylogPowerObserver")
			def := user32.NewProc("DefWindowProcW")
			proc := windows.NewCallback(func(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
				if msg == 0x218 && wparam == 0x8013 && lparam != 0 { // WM_POWERBROADCAST / PBT_POWERSETTINGCHANGE
					setting := (*struct {
						ID     windows.GUID
						Length uint32
						Value  uint32
					})(unsafe.Pointer(lparam))
					if setting.Length == 4 && setting.ID == displayGUID {
						displayOff.Store(setting.Value == 0)
					}
				}
				r, _, _ := def.Call(hwnd, uintptr(msg), wparam, lparam)
				return r
			})
			instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
			class := struct {
				Size, Style                   uint32
				Proc                          uintptr
				ClassExtra, WindowExtra       int32
				Instance, Icon, Cursor, Brush uintptr
				Menu, Name                    *uint16
				SmallIcon                     uintptr
			}{Proc: proc, Name: name, Instance: instance}
			class.Size = uint32(unsafe.Sizeof(class))
			if atom, _, _ := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
				return
			}
			hwnd, _, _ := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, instance, 0)
			if hwnd == 0 {
				return
			}
			defer user32.NewProc("DestroyWindow").Call(hwnd)
			powerWindow.Store(hwnd)
			defer powerWindow.Store(0)
			notice, _, _ := user32.NewProc("RegisterPowerSettingNotification").Call(hwnd, uintptr(unsafe.Pointer(&displayGUID)), 0)
			if notice == 0 {
				return
			}
			defer user32.NewProc("UnregisterPowerSettingNotification").Call(notice)
			powerReadyOnce.Do(func() { close(powerReady) })
			msg := struct {
				Window         uintptr
				Message        uint32
				Wparam, Lparam uintptr
				Time           uint32
				X, Y           int32
				Private        uint32
			}{}
			for {
				ok, _, _ := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
				if int32(ok) <= 0 {
					return
				}
				user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
			}
		}()
	})
}

var displayGUID = windows.GUID{Data1: 0x6fe69556, Data2: 0x704a, Data3: 0x47a0, Data4: [8]byte{0x8f, 0x24, 0xc2, 0x8d, 0x93, 0x6f, 0xda, 0x47}}
