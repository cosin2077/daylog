//go:build !windows

package native

import "errors"

func NotifyAnalysisFailure(message string) error {
	return errors.New("Windows notification unavailable")
}
