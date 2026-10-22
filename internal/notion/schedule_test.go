package notion

import (
	"reflect"
	"testing"
)

func TestSyncTimesNormalizeValidateAndPersist(t *testing.T) {
	got, err := ParseSyncTimes("22:00, 12:00,18:00,16:00,20:00,12:00")
	want := []string{"12:00", "16:00", "18:00", "20:00", "22:00"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"24:00", "12:60", "12", "1:00", "12:00,", "12:00,,16:00"} {
		if _, err := ParseSyncTimes(bad); err == nil {
			t.Fatal("invalid schedule accepted", bad)
		}
	}
	settings, err := NewSettings("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Asia/Shanghai", "2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	settings.SyncTimes = got
	root := t.TempDir()
	if err := SaveSettings(root, settings); err != nil {
		t.Fatal(err)
	}
	reloaded, err := ReadSettings(root)
	if err != nil || !reflect.DeepEqual(reloaded.SyncTimes, want) {
		t.Fatal(reloaded.SyncTimes, err)
	}
	minutes, err := reloaded.Checkpoints()
	if err != nil || !reflect.DeepEqual(minutes, []int{720, 960, 1080, 1200, 1320}) {
		t.Fatal(minutes, err)
	}
	clear, err := ParseSyncTimes("")
	if err != nil || len(clear) != 0 {
		t.Fatal(clear, err)
	}
}
