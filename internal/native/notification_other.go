//go:build !windows

package native

import "errors"

func NotifyAnalysisFailure() error { return errors.New("Windows notification unavailable") }
