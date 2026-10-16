//go:build darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestNativeQueryFailureIsActionableAndDoesNotInventPermissionState(t *testing.T) {
	root := t.TempDir()
	_, err := nativeAppQuery(context.Background(), root, "/fixture/Daylog.app", []string{"doctor", "--json"}, func(context.Context, []string) error { return errors.New("fixture LaunchServices failure") })
	if err == nil {
		t.Fatal("launch failure accepted")
	}
	for _, want := range []string{"/fixture/Daylog.app", "下一步", "访达", "codesign", "daylog doctor"} {
		if !strings.Contains(explainError(err).Error(), want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
	var output bytes.Buffer
	if err := writeDoctorFailure(&output, root, err); err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(output.Bytes(), &result) != nil || string(result["ok"]) != "false" || result["environment"] != nil {
		t.Fatalf("failure fabricated a healthy environment: %s", output.String())
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatal("native query temporary files retained")
	}
}

func TestNativeQueryReadsOnlyItsOwnResult(t *testing.T) {
	data, err := nativeAppQuery(context.Background(), t.TempDir(), "/fixture/Daylog.app", []string{"doctor", "--json"}, func(_ context.Context, args []string) error {
		for i, arg := range args {
			if arg == "--stdout" {
				return os.WriteFile(args[i+1], []byte(`{"fixture":true}`), 0600)
			}
		}
		return errors.New("missing result path")
	})
	if err != nil || string(data) != `{"fixture":true}` {
		t.Fatalf("query result lost: %s (%v)", data, err)
	}
}
