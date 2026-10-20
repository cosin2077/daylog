//go:build windows

package native

import (
	"errors"
	"golang.org/x/sys/windows"
	"unsafe"
)

func namedTarget(name string) *uint16 { p, _ := windows.UTF16PtrFromString("Daylog/" + name); return p }
func SetNamedSecret(name, value string) error {
	if err := validateNamedSecret(name, value); err != nil {
		return err
	}
	if value == "" {
		return errors.New("凭证不能为空")
	}
	raw := []byte(value)
	c := credential{Type: 1, Target: namedTarget(name), Size: uint32(len(raw)), Blob: &raw[0], Persist: 2}
	if ok, _, _ := credentials.NewProc("CredWriteW").Call(uintptr(unsafe.Pointer(&c)), 0); ok == 0 {
		return errors.New("Windows 凭证存储写入失败")
	}
	return nil
}
func NamedSecret(name string) string {
	if validateNamedSecret(name, "") != nil {
		return ""
	}
	var c *credential
	if ok, _, _ := credentials.NewProc("CredReadW").Call(uintptr(unsafe.Pointer(namedTarget(name))), 1, 0, uintptr(unsafe.Pointer(&c))); ok == 0 || c == nil {
		return ""
	}
	defer credentials.NewProc("CredFree").Call(uintptr(unsafe.Pointer(c)))
	if c.Size == 0 || c.Size > 2560 || c.Blob == nil {
		return ""
	}
	return string(unsafe.Slice(c.Blob, int(c.Size)))
}
func DeleteNamedSecret(name string) error {
	if err := validateNamedSecret(name, ""); err != nil {
		return err
	}
	if ok, _, err := credentials.NewProc("CredDeleteW").Call(uintptr(unsafe.Pointer(namedTarget(name))), 1, 0); ok == 0 && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return errors.New("Windows 凭证删除失败")
	}
	return nil
}
