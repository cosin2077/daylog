//go:build windows

package native

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var gdi32 = windows.NewLazySystemDLL("gdi32.dll")
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")
var wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
var foreground = user32.NewProc("GetForegroundWindow")
var windowPID = user32.NewProc("GetWindowThreadProcessId")
var lastInput = user32.NewProc("GetLastInputInfo")
var tickCount = kernel32.NewProc("GetTickCount")
var systemPower = kernel32.NewProc("GetSystemPowerStatus")
var openDesktop = user32.NewProc("OpenInputDesktop")
var closeDesktop = user32.NewProc("CloseDesktop")
var objectInfo = user32.NewProc("GetUserObjectInformationW")
var querySession = wtsapi32.NewProc("WTSQuerySessionInformationW")
var freeSession = wtsapi32.NewProc("WTSFreeMemory")
var getDC = user32.NewProc("GetDC")
var releaseDC = user32.NewProc("ReleaseDC")
var compatibleDC = gdi32.NewProc("CreateCompatibleDC")
var deleteDC = gdi32.NewProc("DeleteDC")
var createDIB = gdi32.NewProc("CreateDIBSection")
var deleteObject = gdi32.NewProc("DeleteObject")
var selectObject = gdi32.NewProc("SelectObject")
var stretchBlt = gdi32.NewProc("StretchBlt")
var stretchMode = gdi32.NewProc("SetStretchBltMode")
var brushOrigin = gdi32.NewProc("SetBrushOrgEx")
var flushGDI = gdi32.NewProc("GdiFlush")
var metrics = user32.NewProc("GetSystemMetrics")
var clientRect = user32.NewProc("GetClientRect")
var clientToScreen = user32.NewProc("ClientToScreen")
var dpiContext = user32.NewProc("SetThreadDpiAwarenessContext")
var enumWindows = user32.NewProc("EnumWindows")
var windowVisible = user32.NewProc("IsWindowVisible")
var windowIconic = user32.NewProc("IsIconic")
var windowText = user32.NewProc("GetWindowTextW")
var windowRect = user32.NewProc("GetWindowRect")

// NewCallback allocates a runtime slot that cannot be freed. Reuse one callback
// for frequent capture checks instead of allocating two slots per screenshot.
var windowEnumerationMu sync.Mutex
var windowVisitor func(uintptr) bool
var windowEnumerationCallback = windows.NewCallback(func(hwnd, unused uintptr) uintptr {
	if windowVisitor(hwnd) {
		return 1
	}
	return 0
})

func visitWindows(visitor func(uintptr) bool) error {
	windowEnumerationMu.Lock()
	defer windowEnumerationMu.Unlock()
	stopped := false
	windowVisitor = func(hwnd uintptr) bool {
		if !visitor(hwnd) {
			stopped = true
			return false
		}
		return true
	}
	defer func() { windowVisitor = nil }()
	if ok, _, _ := enumWindows.Call(windowEnumerationCallback, 0); ok == 0 && !stopped {
		return errors.New("window enumeration failed")
	}
	return nil
}

func visiblePrivacyWindows() ([]privacyWindow, error) {
	var list []privacyWindow
	err := visitWindows(func(hwnd uintptr) bool {
		visible, _, _ := windowVisible.Call(hwnd)
		minimized, _, _ := windowIconic.Call(hwnd)
		if visible == 0 || minimized != 0 {
			return true
		}
		r := struct{ Left, Top, Right, Bottom int32 }{}
		ok, _, _ := windowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		list = append(list, privacyWindow{App: windowApp(hwnd), Bounds: image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)), BoundsKnown: ok != 0})
		return true
	})
	return list, err
}

func inspectCapturePrivacy(region image.Rectangle, blocked []string) error {
	if len(blocked) == 0 {
		return nil
	}
	windows, err := visiblePrivacyWindows()
	if err != nil {
		return ErrBlocked
	}
	return checkCapturePrivacy(region, windows, blocked)
}

func windowApp(hwnd uintptr) string {
	var pid uint32
	windowPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if e != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) != nil {
		return ""
	}
	return strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:size])))
}

// Never switch desktops or request access to the secure sign-in/UAC desktop.
func desktopAvailable() bool {
	var session uint32
	if windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session) != nil || session == 0 {
		return false
	}
	var data *byte
	var size uint32
	ok, _, _ := querySession.Call(0, uintptr(session), 8, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&size))) // WTSConnectState
	if ok == 0 {
		return false
	}
	defer freeSession.Call(uintptr(unsafe.Pointer(data)))
	if size < 4 || *(*uint32)(unsafe.Pointer(data)) != 0 {
		return false
	} // WTSActive
	d, _, _ := openDesktop.Call(0, 0, 1)
	if d == 0 {
		return false
	}
	defer closeDesktop.Call(d)
	name := make([]uint16, 256)
	var needed uint32
	ok, _, _ = objectInfo.Call(d, 2, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)*2), uintptr(unsafe.Pointer(&needed)))
	return ok != 0 && strings.EqualFold(windows.UTF16ToString(name), "Default")
}

func (Desktop) Environment(ctx context.Context) (Environment, error) {
	if e := ctx.Err(); e != nil {
		return Environment{}, e
	}
	startPowerObserver()
	env := Environment{Permission: true, BatteryPercent: -1, Thermal: -1, Locked: !desktopAvailable(), ScreenOff: displayOff.Load()}
	if !env.Locked {
		h, _, _ := foreground.Call()
		env.App = windowApp(h)
	}
	input := struct{ Size, Tick uint32 }{Size: 8}
	if ok, _, _ := lastInput.Call(uintptr(unsafe.Pointer(&input))); ok != 0 {
		now, _, _ := tickCount.Call()
		env.IdleSeconds = float64(uint32(now)-input.Tick) / 1000
	} else {
		return env, errors.New("idle state unavailable")
	}
	power := struct {
		AC, Battery, Percent, Saver byte
		Remaining, Full             uint32
	}{}
	if ok, _, _ := systemPower.Call(uintptr(unsafe.Pointer(&power))); ok == 0 {
		return env, errors.New("power state unavailable")
	}
	env.OnBattery = power.AC != 1 // Unknown AC state uses the conservative battery policy.
	if power.Percent <= 100 {
		env.BatteryPercent = int(power.Percent)
	}
	env.LowPower = power.Saver == 1 || power.Battery != 255 && power.Battery&2 != 0
	if power.Battery != 255 && power.Battery&4 != 0 {
		if env.BatteryPercent < 0 || env.BatteryPercent > 10 {
			env.BatteryPercent = 10
		}
	}
	return env, nil
}

func captureAllowed(env Environment, blocked []string) error {
	if env.Locked || env.ScreenOff {
		return errors.New("interactive desktop unavailable")
	}
	if env.App == "" {
		return errors.New("foreground application unavailable")
	}
	if blockedApplication(env.App, blocked) {
		return ErrBlocked
	}
	return nil
}

func (d Desktop) Capture(ctx context.Context, opts CaptureOptions) ([]byte, error) {
	if opts.Height < 1 || opts.Height > 2160 || opts.Quality < 1 || opts.Quality > 100 {
		return nil, errors.New("invalid capture size or quality")
	}
	env, e := d.Environment(ctx)
	if e != nil {
		return nil, e
	}
	if e = captureAllowed(env, opts.Blocked); e != nil {
		return nil, e
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Keep screen/client coordinates in physical pixels on mixed-DPI desktops.
	if dpiContext.Find() == nil {
		old, _, _ := dpiContext.Call(^uintptr(3))
		if old != 0 {
			defer dpiContext.Call(old)
		}
	}
	width, _, _ := metrics.Call(0)
	height, _, _ := metrics.Call(1)
	x, y := int32(0), int32(0)
	w, h := int(width), int(height)
	if opts.WindowID != 0 {
		hwnd := uintptr(opts.WindowID)
		fg, _, _ := foreground.Call()
		visible, _, _ := windowVisible.Call(hwnd)
		minimized, _, _ := windowIconic.Call(hwnd)
		if hwnd != fg || visible == 0 || minimized != 0 {
			return nil, errors.New("selected window must be visible and foreground")
		}
		r := struct{ Left, Top, Right, Bottom int32 }{}
		point := struct{ X, Y int32 }{}
		if ok, _, _ := clientRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
			return nil, errors.New("window bounds unavailable")
		}
		if ok, _, _ := clientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&point))); ok == 0 {
			return nil, errors.New("window position unavailable")
		}
		x, y = point.X, point.Y
		w, h = int(r.Right-r.Left), int(r.Bottom-r.Top)
	}
	if w <= 0 || h <= 0 || w > 32768 || h > 32768 {
		return nil, errors.New("screen dimensions unavailable")
	}
	region := image.Rect(int(x), int(y), int(x)+w, int(y)+h)
	if err := inspectCapturePrivacy(region, opts.Blocked); err != nil {
		return nil, err
	}
	outh := min(h, opts.Height)
	outw := max(1, w*outh/h)
	if outw > 4096 {
		outw = 4096
		outh = max(1, h*outw/w)
	}
	screen, _, _ := getDC.Call(0)
	if screen == 0 {
		return nil, errors.New("screen DC unavailable")
	}
	defer releaseDC.Call(0, screen)
	mem, _, _ := compatibleDC.Call(screen)
	if mem == 0 {
		return nil, errors.New("capture DC unavailable")
	}
	defer deleteDC.Call(mem)
	info := struct {
		Size                   uint32
		Width, Height          int32
		Planes, Bits           uint16
		Compression, ImageSize uint32
		XPels, YPels           int32
		Used, Important        uint32
	}{Size: 40, Width: int32(outw), Height: -int32(outh), Planes: 1, Bits: 32}
	var pixels unsafe.Pointer
	bitmap, _, _ := createDIB.Call(screen, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if bitmap == 0 || pixels == nil {
		return nil, errors.New("capture bitmap unavailable")
	}
	defer deleteObject.Call(bitmap)
	old, _, _ := selectObject.Call(mem, bitmap)
	if old == 0 || old == ^uintptr(0) {
		return nil, errors.New("capture bitmap selection failed")
	}
	defer selectObject.Call(mem, old)
	stretchMode.Call(mem, 4)
	brushOrigin.Call(mem, 0, 0, 0)
	ok, _, _ := stretchBlt.Call(mem, 0, 0, uintptr(outw), uintptr(outh), screen, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0x00CC0020|0x40000000)
	if ok == 0 {
		return nil, errors.New("desktop capture failed")
	}
	flushGDI.Call()
	// Re-check privacy after capture; a foreground transition discards the image.
	after, e := d.Environment(ctx)
	if e != nil {
		return nil, e
	}
	if e = captureAllowed(after, opts.Blocked); e != nil {
		return nil, e
	}
	if after.App != env.App {
		return nil, errors.New("foreground changed during capture")
	}
	if opts.WindowID != 0 {
		fg, _, _ := foreground.Call()
		if fg != uintptr(opts.WindowID) {
			return nil, errors.New("selected window changed during capture")
		}
		r := struct{ Left, Top, Right, Bottom int32 }{}
		point := struct{ X, Y int32 }{}
		ok, _, _ := clientRect.Call(fg, uintptr(unsafe.Pointer(&r)))
		position, _, _ := clientToScreen.Call(fg, uintptr(unsafe.Pointer(&point)))
		if ok == 0 || position == 0 || point.X != x || point.Y != y || int(r.Right-r.Left) != w || int(r.Bottom-r.Top) != h {
			return nil, errors.New("selected window moved during capture")
		}
	}
	if err := inspectCapturePrivacy(region, opts.Blocked); err != nil {
		return nil, err
	}
	raw := unsafe.Slice((*byte)(pixels), outw*outh*4)
	im := image.NewRGBA(image.Rect(0, 0, outw, outh))
	for i := 0; i < len(raw); i += 4 {
		im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = raw[i+2], raw[i+1], raw[i], 255
	}
	var buf bytes.Buffer
	e = jpeg.Encode(&buf, im, &jpeg.Options{Quality: opts.Quality})
	return buf.Bytes(), e
}

func RequestPermission() bool { return desktopAvailable() }
func Windows() ([]Window, error) {
	if !desktopAvailable() {
		return nil, errors.New("interactive desktop unavailable")
	}
	var list []Window
	err := visitWindows(func(hwnd uintptr) bool {
		visible, _, _ := windowVisible.Call(hwnd)
		if visible == 0 {
			return true
		}
		app := windowApp(hwnd)
		if app == "" {
			return true
		}
		text := make([]uint16, 512)
		n, _, _ := windowText.Call(hwnd, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
		if n == 0 {
			return true
		}
		list = append(list, Window{ID: uint32(hwnd), App: app, Title: windows.UTF16ToString(text)})
		return true
	})
	if err != nil {
		return nil, err
	}
	return list, nil
}

const AppSource = "win32_foreground"
