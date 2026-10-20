//go:build darwin && cgo

package native

/*
#include <stdlib.h>
int dg_named_secret_set(const char *name,const char *value);
char *dg_named_secret_get(const char *name);
int dg_named_secret_delete(const char *name);
*/
import "C"
import (
	"errors"
	"unsafe"
)

func SetNamedSecret(name, value string) error {
	if err := validateNamedSecret(name, value); err != nil {
		return err
	}
	if value == "" {
		return errors.New("凭证不能为空")
	}
	n, v := C.CString(name), C.CString(value)
	defer C.free(unsafe.Pointer(n))
	defer C.free(unsafe.Pointer(v))
	if C.dg_named_secret_set(n, v) != 0 {
		return errors.New("无法写入系统钥匙串")
	}
	return nil
}
func NamedSecret(name string) string {
	if validateNamedSecret(name, "") != nil {
		return ""
	}
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))
	p := C.dg_named_secret_get(n)
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}
func DeleteNamedSecret(name string) error {
	if err := validateNamedSecret(name, ""); err != nil {
		return err
	}
	n := C.CString(name)
	defer C.free(unsafe.Pointer(n))
	if C.dg_named_secret_delete(n) != 0 {
		return errors.New("无法删除系统钥匙串凭证")
	}
	return nil
}
