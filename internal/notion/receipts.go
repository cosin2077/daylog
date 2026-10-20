package notion

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"daylog/internal/journal"
)

type receipt struct {
	Pending bool   `json:"pending"`
	ID      string `json:"id,omitempty"`
}

func (s *Service) createOnce(ctx context.Context, key, path string, body any, find func() (map[string]any, error)) (map[string]any, error) {
	if found, err := find(); err != nil {
		return nil, err
	} else if found != nil {
		return found, nil
	}
	root := s.Settings.StateRoot(s.Root)
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	file := filepath.Join(root, "receipts.json")
	records := map[string]receipt{}
	if err := journal.ReadJSON(file, &records); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	r := records[key]
	if r.Pending && !s.AllowUncertain {
		if r.ID != "" {
			found, err := s.Client.request(ctx, "GET", path+"/"+r.ID, nil)
			if err == nil {
				return found, nil
			}
		}
		return nil, &Error{Code: "uncertain_write"}
	}
	records[key] = receipt{Pending: true}
	if err := journal.WriteJSON(file, records); err != nil {
		return nil, err
	}
	result, err := s.Client.request(ctx, "POST", path, body)
	if err != nil {
		var api *Error
		if errors.As(err, &api) {
			if api.Uncertain {
				records[key] = receipt{Pending: true, ID: api.CommittedID}
			} else {
				delete(records, key)
			}
		}
		if saveErr := journal.WriteJSON(file, records); saveErr != nil {
			return nil, errors.Join(err, saveErr)
		}
		return nil, err
	}
	if str(result["id"]) == "" {
		return nil, &Error{Code: "uncertain_write"}
	}
	records[key] = receipt{ID: str(result["id"])}
	if err = journal.WriteJSON(file, records); err != nil {
		return nil, err
	}
	return result, nil
}
