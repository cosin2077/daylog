package notion

import (
	"strings"
	"testing"

	"daylog/internal/exports"
	"daylog/internal/journal"
)

func TestDayContentDistinguishesWorkAndTaskEstimates(t *testing.T) {
	d := exports.Day{Date: "2026-10-24", Timezone: "Asia/Shanghai", EstimatedSeconds: 60, WorkTime: journal.WorkTime{Available: true, EstimatedSeconds: 600, ClassifiedSeconds: 60, UnclassifiedSeconds: 540}}
	content := renderDay(d)
	for _, text := range []string{"电脑工作估时：10分钟", "已归类 1分钟，未归类 9分钟", "只统计已整理来源", "采集异常不推断为离开", "估计投入：1分钟"} {
		if !strings.Contains(content, text) {
			t.Fatal("ambiguous work accounting in daily export", text)
		}
	}
}
