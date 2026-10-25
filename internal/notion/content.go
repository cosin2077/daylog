package notion

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

	"daylog/internal/exports"
)

const recordHeading = "## Daylog 活动记录"
const notesHeading = "## 我的备注"

var markdownEscapes = strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "#", "\\#")
var markdownAutoLinks = regexp.MustCompile(`\[((?:\\.|[^\[\]\\\n])*)\]\((https?://[^)\n]+)\)`)

func readableText(value string) string {
	return markdownEscapes.Replace(strings.Join(strings.Fields(value), " "))
}
func readableSeconds(value int64) string {
	if value <= 0 {
		return "0秒"
	}
	hours, minutes, seconds := value/3600, value/60%60, value%60
	var parts []string
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d小时", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%d分钟", minutes))
	}
	if seconds > 0 {
		parts = append(parts, fmt.Sprintf("%d秒", seconds))
	}
	return strings.Join(parts, "")
}
func timeLabel(at time.Time) string {
	if at.Second() != 0 {
		return at.Format("15:04:05")
	}
	return at.Format("15:04")
}

// Plain, date-local observations are the export. Model explanations are not
// promoted to confirmed time, and technical metadata stays in local state.
func renderDay(day exports.Day) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n记录日期：%s；时区：%s。", recordHeading, day.Date, readableText(day.Timezone))
	if day.Ongoing {
		b.WriteString("今天尚未结束，本页为当前已有记录，之后会继续补齐。")
	}
	fmt.Fprintf(&b, "\n\n已整理 %d 段，%d 条活动；待补齐 %d 段（其中失败 %d 段），未封批图片 %d 张。", day.AnalyzedBatches, len(day.Activities), day.PendingBatches, day.FailedBatches, day.DraftFrames)
	if day.WorkTime.Available {
		fmt.Fprintf(&b, "\n\n电脑工作估时：%s；已归类任务 %s，仅能确定应用 %s，其他未归类 %s。仅应用和其他未归类都已计入工作估时，不代表已确认具体任务；这里只统计已整理来源。按采集时的无输入阈值处理离开，锁屏、息屏及休眠不计工作，采集异常不推断为离开。", readableSeconds(day.WorkTime.EstimatedSeconds), readableSeconds(day.WorkTime.ClassifiedSeconds), readableSeconds(day.WorkTime.AppAttributedSeconds), readableSeconds(day.WorkTime.OtherUnclassifiedSeconds))
	}
	fmt.Fprintf(&b, "\n\n已归类活动估时：%s；其中插值 %s、邻接推断 %s，均已包含在已归类估时中，不应再次相加。任务估时缺口 %s；旧版范围覆盖 %s 单列。采集空白不等于休息，估时不等于确认工时。", readableSeconds(day.EstimatedSeconds), readableSeconds(day.InterpolatedSeconds), readableSeconds(day.InferredSeconds), readableSeconds(day.UnknownSeconds), readableSeconds(day.LegacyCoverageSeconds))
	for _, a := range day.Activities {
		when := timeLabel(a.Start)
		if a.End.After(a.Start) {
			when += "–" + timeLabel(a.End)
		}
		fmt.Fprintf(&b, "\n\n### %s｜%s", when, readableText(a.Title))
		if a.Summary != "" {
			fmt.Fprintf(&b, "\n\n%s", readableText(a.Summary))
		}
		if a.Task != "" && a.Task != a.Title {
			fmt.Fprintf(&b, "\n\n任务：%s。", readableText(a.Task))
		}
		fmt.Fprintf(&b, "\n\n类别：%s；应用：%s。", readableText(a.Category), readableText(strings.Join(a.Apps, "、")))
		switch {
		case a.DurationMethod == "legacy_model_range":
			b.WriteString("旧版范围，未计入新的观察估时。")
		case a.EstimatedSeconds == 0:
			b.WriteString("持续时长未估计；单个观察点不分配时长。")
		default:
			fmt.Fprintf(&b, "估计投入 %s。", readableSeconds(a.EstimatedSeconds))
		}
		if len(a.Spans) > 1 || a.InferredSeconds > 0 || a.InterpolatedSeconds > 0 {
			var parts []string
			for _, span := range a.Spans {
				part := timeLabel(span.Start) + "–" + timeLabel(span.End)
				if span.Inferred {
					part += "（邻接推断）"
				} else if span.Interpolated {
					part += "（关键帧插值）"
				}
				parts = append(parts, part)
			}
			fmt.Fprintf(&b, "\n\n估时区间：%s。", strings.Join(parts, "、"))
		}
		if len(a.Actions) > 0 {
			fmt.Fprintf(&b, "\n\n具体动作：%s。", readableText(strings.Join(a.Actions, "；")))
		}
		if a.Confidence == "low" {
			b.WriteString("\n\n本条活动判断的置信度较低。")
		}
		if len(a.Uncertainties) > 0 {
			fmt.Fprintf(&b, "\n\n不确定性：%s", readableText(strings.Join(a.Uncertainties, "；")))
		}
	}
	return b.String()
}

func canonicalMarkdown(value string) string {
	// Notion also linkifies bare domains and filenames such as cache.rs or
	// setup.sh. Accept only a target equal to the original text, optionally
	// prefixed with the HTTP(S) scheme supplied by Notion.
	value = markdownAutoLinks.ReplaceAllStringFunc(value, func(link string) string {
		parts := markdownAutoLinks.FindStringSubmatch(link)
		if parts[1] == parts[2] || "http://"+parts[1] == parts[2] || "https://"+parts[1] == parts[2] {
			return parts[1]
		}
		return link
	})
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	value = html.UnescapeString(strings.Join(lines, "\n"))
	var plain strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) && strings.ContainsRune(`\`+"`*_[]<>#!", rune(value[i+1])) {
			i++
		}
		plain.WriteByte(value[i])
	}
	return plain.String()
}

func managedContent(markdown string) (string, error) {
	start := regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(recordHeading)+`[\t ]*$`).FindAllStringIndex(markdown, -1)
	end := regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(notesHeading)+`[\t ]*$`).FindAllStringIndex(markdown, -1)
	if len(start) != 1 || len(end) != 1 || start[0][0] >= end[0][0] {
		return "", errors.New("页面缺少唯一的 Daylog 活动记录／我的备注区域，已保留正文；请恢复这两个标题后重试")
	}
	return strings.TrimRight(markdown[start[0][0]:end[0][0]], "\n"), nil
}

func (s *Service) updateContent(ctx context.Context, pageID, generated string) error {
	path := "/pages/" + pageID + "/markdown"
	current, err := s.Client.request(ctx, "GET", path, nil)
	if err != nil {
		return err
	}
	if current["truncated"] == true || len(items(current["unknown_block_ids"])) > 0 {
		return errors.New("Notion 正文未完整读取，已保留云端内容；请检查页面大小和访问权限")
	}
	old, err := managedContent(str(current["markdown"]))
	if err != nil {
		return err
	}
	if canonicalMarkdown(old) == canonicalMarkdown(generated) {
		return nil
	}
	updated, err := s.Client.request(ctx, "PATCH", path, map[string]any{
		"type": "update_content", "update_content": map[string]any{"content_updates": []any{
			map[string]any{"old_str": old, "new_str": generated},
		}},
	})
	if err != nil {
		return err
	}
	if updated["truncated"] == true || len(items(updated["unknown_block_ids"])) > 0 {
		return errors.New("Notion 已响应写入但未返回完整正文，下次同步会重新核验")
	}
	actual, err := managedContent(str(updated["markdown"]))
	if err != nil || canonicalMarkdown(actual) != canonicalMarkdown(generated) {
		return errors.New("Notion 正文写入尚未完整确认，下次同步会重新核验")
	}
	return nil
}

func (c *Client) children(ctx context.Context, parent string) ([]map[string]any, error) {
	var all []map[string]any
	cursor := ""
	for {
		path := "/blocks/" + parent + "/children?page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + url.QueryEscape(cursor)
		}
		data, err := c.request(ctx, "GET", path, nil)
		if err != nil {
			return nil, err
		}
		for _, item := range items(data["results"]) {
			all = append(all, obj(item))
		}
		if data["has_more"] != true {
			return all, nil
		}
		next := str(data["next_cursor"])
		if next == "" || next == cursor {
			return nil, errors.New("Notion 分页游标无效")
		}
		cursor = next
	}
}
