package journal

import (
	"context"
	"os"
)

// Preparation failures belong to a batch but are not provider calls. The round
// coordinator records one backoff regardless of configured provider fallbacks.
type preparationError struct{ reason string }

func (e *preparationError) Error() string { return FailureReason(e.reason) }

func (s *Store) analysisImage(ctx context.Context, f Frame) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.FramePath(f)
	if err != nil {
		return nil, &preparationError{"evidence_unavailable"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &preparationError{"evidence_unavailable"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if checkFrameDigest(f, data) != nil {
		return nil, &preparationError{"evidence_digest_mismatch"}
	}
	if _, err = FingerprintJPEG(data); err != nil {
		return nil, &preparationError{"evidence_invalid_image"}
	}
	return data, nil
}
