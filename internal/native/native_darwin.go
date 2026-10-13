//go:build darwin && cgo

package native

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework Foundation -framework AppKit -framework ScreenCaptureKit -framework CoreGraphics -framework ImageIO -framework IOKit -framework Security
#include <stdlib.h>
char *dg_environment(void);
int dg_capture(int height, int quality, unsigned int windowID, const char *blocked, void **bytes, int *length);
char *dg_windows(void);
int dg_permission(void);
int dg_secret_set(const char *value);
char *dg_secret_get(void);
int dg_secret_delete(void);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"unsafe"
)

func (Mac) Environment(ctx context.Context) (Environment, error) {
	var env Environment
	if err := ctx.Err(); err != nil {
		return env, err
	}
	p := C.dg_environment()
	if p == nil {
		return env, errors.New("native environment query failed")
	}
	defer C.free(unsafe.Pointer(p))
	err := json.Unmarshal([]byte(C.GoString(p)), &env)
	return env, err
}

func (Mac) Capture(ctx context.Context, opts CaptureOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(opts.Blocked)
	blocked := C.CString(string(raw))
	defer C.free(unsafe.Pointer(blocked))
	var p unsafe.Pointer
	var length C.int
	result := C.dg_capture(C.int(opts.Height), C.int(opts.Quality), C.uint(opts.WindowID), blocked, &p, &length)
	if p != nil {
		defer C.free(p)
	}
	if result != 0 {
		if result == 4 {
			return nil, ErrBlocked
		}
		messages := map[int]string{1: "screen recording permission required", 2: "screen capture timed out", 3: "screen capture failed", 4: "foreground app blocked", 5: "screen unavailable"}
		message := messages[int(result)]
		if message == "" {
			message = "native capture failed"
		}
		return nil, errors.New(message)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return C.GoBytes(p, length), nil
}

func RequestPermission() bool { return C.dg_permission() != 0 }
func Windows() ([]Window, error) {
	p := C.dg_windows()
	if p == nil {
		return nil, errors.New("window list unavailable; check screen permission")
	}
	defer C.free(unsafe.Pointer(p))
	var out []Window
	err := json.Unmarshal([]byte(C.GoString(p)), &out)
	return out, err
}
func SetSecret(value string) error {
	p := C.CString(value)
	defer C.free(unsafe.Pointer(p))
	if C.dg_secret_set(p) != 0 {
		return errors.New("Keychain write failed")
	}
	return nil
}
func Secret() string {
	p := C.dg_secret_get()
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}
func DeleteSecret() error {
	if C.dg_secret_delete() != 0 {
		return errors.New("Keychain deletion failed")
	}
	return nil
}
