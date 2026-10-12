//go:build !darwin || !cgo

package native

import (
	"context"
	"errors"
)

func (Mac) Environment(context.Context) (Environment, error) {
	return Environment{}, errors.New("native capture requires macOS 14+ and a cgo build")
}
func (Mac) Capture(context.Context, CaptureOptions) ([]byte, error) {
	return nil, errors.New("native capture unavailable")
}
func RequestPermission() bool    { return false }
func Windows() ([]Window, error) { return nil, errors.New("window selection unavailable") }
func SetSecret(string) error     { return errors.New("Keychain unavailable") }
func Secret() string             { return "" }
func DeleteSecret() error        { return errors.New("Keychain unavailable") }
