package notion

import (
	"strings"
	"testing"

	"daylog/internal/exports"
	"daylog/internal/journal"
)

func TestDayContentDistinguishesWorkAndTaskEstimates(t *testing.T) {
	d := exports.Day{Date: "2026-10-24", Timezone: "Asia/Shanghai", EstimatedSeconds: 60, WorkTime: journal.WorkTime{Available: true, EstimatedSeconds: 600, ClassifiedSeconds: 60, AppAttributedSeconds: 240, OtherUnclassifiedSeconds: 300, UnclassifiedSeconds: 540}}
	content := renderDay(d)
	for _, text := range []string{"电脑工作估时：10分钟", "已归类任务 1分钟，仅能确定应用 4分钟，其他未归类 5分钟", "只统计已整理来源", "采集异常不推断为离开", "已归类活动估时：1分钟"} {
		if !strings.Contains(content, text) {
			t.Fatal("ambiguous work accounting in daily export", text)
		}
	}
}
