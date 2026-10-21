package notion

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"daylog/internal/journal"
)

type receipt struct {
	Pending bool   `json:"pending"`
	ID      string `json:"id,omitempty"`
}

var receiptDate = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`)

// Shard by source date so successful IDs remain available for reconciliation
// without growing one lifetime JSON file. Setup has its own tiny bucket.
func (s *Service) receiptFile(key string) string {
	bucket := receiptDate.FindString(key)
	if bucket == "" {
		bucket = "setup"
	}
	return filepath.Join(s.Settings.StateRoot(s.Root), "receipts", bucket+".json")
}
func readReceipts(path string) (map[string]receipt, error) {
	records := map[string]receipt{}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// A date bucket is bounded by daily recording budgets. Use an explicit
	// bound here rather than the general 2 MiB journal helper.
	data, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, errors.New("Notion 单日回执过大，请检查重复导入的记录")
	}
	err = json.Unmarshal(data, &records)
	return records, err
}
func writeReceipts(path string, records map[string]receipt) error {
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > 16<<20 {
		return errors.New("Notion 单日回执达到上限，未发出新建请求")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return journal.WriteJSON(path, records)
}
func (s *Service) createOnce(ctx context.Context, key, path string, body any, find func() (map[string]any, error)) (map[string]any, error) {
	if found, err := find(); err != nil {
		return nil, err
	} else if found != nil {
		return found, nil
	}
	file := s.receiptFile(key)
	records, err := readReceipts(file)
	if err != nil {
		return nil, err
	}
	r := records[key]
	// A successful receipt matters too: a query may temporarily omit a page.
	// Retrieve its known ID before considering another create.
	if r.ID != "" {
		found, getErr := s.Client.request(ctx, "GET", path+"/"+r.ID, nil)
		if getErr == nil && found != nil {
			if path == "/pages" {
				want := obj(body)
				date := func(page map[string]any) string {
					return str(obj(obj(obj(page["properties"])["日期"])["date"])["start"])
				}
				if !sameID(str(obj(found["parent"])["data_source_id"]), str(obj(want["parent"])["data_source_id"])) || date(found) != date(want) {
					return nil, &Error{Code: "duplicate"}
				}
			}
			return found, nil
		}
		if !s.AllowUncertain {
			return nil, getErr
		}
	}
	if r.Pending && !s.AllowUncertain {
		return nil, &Error{Code: "uncertain_write"}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	records[key] = receipt{Pending: true}
	if err = writeReceipts(file, records); err != nil {
		return nil, err
	}
	result, err := s.Client.request(ctx, "POST", path, body)
	if err != nil {
		var api *Error
		var notSent *requestNotSent
		if errors.As(err, &notSent) {
			delete(records, key)
		} else if errors.As(err, &api) {
			if api.Uncertain {
				records[key] = receipt{Pending: true, ID: api.CommittedID}
			} else {
				delete(records, key)
			}
		}
		if saveErr := writeReceipts(file, records); saveErr != nil {
			return nil, errors.Join(err, saveErr)
		}
		return nil, err
	}
	if str(result["id"]) == "" {
		return nil, &Error{Code: "uncertain_write"}
	}
	records[key] = receipt{ID: str(result["id"])}
	if err = writeReceipts(file, records); err != nil {
		return nil, err
	}
	return result, nil
}
