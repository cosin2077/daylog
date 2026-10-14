//go:build windows

package native

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

var credentials = windows.NewLazySystemDLL("advapi32.dll")

type credential struct {
	Flags, Type             uint32
	Target, Comment         *uint16
	Written                 windows.Filetime
	Size                    uint32
	Blob                    *byte
	Persist, AttributeCount uint32
	Attributes              uintptr
	Alias, User             *uint16
}

func credentialTarget() *uint16 { p, _ := windows.UTF16PtrFromString("Daylog/AI"); return p }
func SetSecret(value string) error {
	if len(value) == 0 || len(value) > 2560 {
		return errors.New("credential must contain 1 to 2560 bytes")
	}
	raw := []byte(value)
	c := credential{Type: 1, Target: credentialTarget(), Size: uint32(len(raw)), Blob: &raw[0], Persist: 2}
	if ok, _, _ := credentials.NewProc("CredWriteW").Call(uintptr(unsafe.Pointer(&c)), 0); ok == 0 {
		return errors.New("Windows credential write failed")
	}
	return nil
}
func Secret() string {
	var c *credential
	if ok, _, _ := credentials.NewProc("CredReadW").Call(uintptr(unsafe.Pointer(credentialTarget())), 1, 0, uintptr(unsafe.Pointer(&c))); ok == 0 || c == nil {
		return ""
	}
	defer credentials.NewProc("CredFree").Call(uintptr(unsafe.Pointer(c)))
	if c.Size > 2560 || c.Size == 0 || c.Blob == nil {
		return ""
	}
	return string(unsafe.Slice(c.Blob, int(c.Size)))
}
func DeleteSecret() error {
	if ok, _, e := credentials.NewProc("CredDeleteW").Call(uintptr(unsafe.Pointer(credentialTarget())), 1, 0); ok == 0 && !errors.Is(e, windows.ERROR_NOT_FOUND) {
		return errors.New("Windows credential deletion failed")
	}
	return nil
}
