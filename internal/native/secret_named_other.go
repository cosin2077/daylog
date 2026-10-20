//go:build (!darwin || !cgo) && !windows

package native

import "errors"

func SetNamedSecret(string, string) error {
	return errors.New("当前平台不支持系统凭证存储")
}
func NamedSecret(string) string      { return "" }
func DeleteNamedSecret(string) error { return errors.New("当前平台不支持系统凭证存储") }
