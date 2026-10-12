//go:build !darwin

package service

import "errors"

func IsLoaded(string) bool { return false }

func Start(string, string) error { return errors.New("LaunchAgent requires macOS") }
func Stop(string) error          { return errors.New("LaunchAgent requires macOS") }
