//go:build !windows

package update

import (
	"context"
	"errors"
)

func installWindows(context.Context, string, string, string, bool, func() error, func() error) (Check, error) {
	return Check{}, errors.New("Windows update unavailable on this OS")
}

func CleanupStaging(string) {}
