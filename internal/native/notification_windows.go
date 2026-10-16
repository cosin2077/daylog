//go:build windows

package native

import (
	"errors"
	"golang.org/x/sys/windows"
	"sync/atomic"
	"time"
	"unsafe"
)

var notificationID atomic.Uint32

func NotifyAnalysisFailure(message string) error {
	startPowerObserver()
	select {
	case <-powerReady:
	case <-time.After(time.Second):
		return errors.New("notification window unavailable")
	}
	hwnd := powerWindow.Load()
	if hwnd == 0 {
		return errors.New("notification window unavailable")
	}
	icon, _, _ := user32.NewProc("LoadIconW").Call(0, 32516)
	data := struct {
		Size                uint32
		Window              uintptr
		ID, Flags, Callback uint32
		Icon                uintptr
		Tip                 [128]uint16
		State, StateMask    uint32
		Info                [256]uint16
		Timeout             uint32
		Title               [64]uint16
		InfoFlags           uint32
		GUID                windows.GUID
		Balloon             uintptr
	}{Window: hwnd, ID: notificationID.Add(1), Flags: 0x2 | 0x4 | 0x10, Icon: icon, Timeout: 15000, InfoFlags: 1 | 0x80}
	data.Size = uint32(unsafe.Sizeof(data))
	tip, _ := windows.UTF16FromString("Daylog")
	copy(data.Tip[:], tip)
	title, _ := windows.UTF16FromString("Daylog · 分析需要关注")
	copy(data.Title[:], title)
	info, _ := windows.UTF16FromString(message)
	copy(data.Info[:], info)
	notify := windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconW")
	if ok, _, _ := notify.Call(0, uintptr(unsafe.Pointer(&data))); ok == 0 {
		return errors.New("Windows notification submission failed")
	}
	go func() { time.Sleep(15 * time.Second); notify.Call(2, uintptr(unsafe.Pointer(&data))) }()
	return nil
}
