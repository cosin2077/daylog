package notion

import (
	"context"
	"errors"
	"strings"
	"time"

	"daylog/internal/exports"
)

func title(text string) map[string]any { return map[string]any{"title": rich(text)} }
func selectValue(text string) map[string]any {
	return map[string]any{"select": map[string]any{"name": text}}
}

func dayProperties(day exports.Day, status string) map[string]any {
	return map[string]any{
		"名称": title(day.Date + " · Daylog"),
		"日期": map[string]any{"date": map[string]any{"start": day.Date}},
		"状态": selectValue(status),
	}
}

// A date is the single remote identity within the configured database. Manual
// previews and midnight finalization therefore update the same page.
func (s *Service) Deliver(ctx context.Context, day exports.Day) (string, error) {
	if s.Settings.Layout != DailyLayout {
		return "", errors.New("Notion 仍为旧版双库配置，请重新运行 daylog notion setup")
	}
	zone, err := time.LoadLocation(day.Timezone)
	if err != nil {
		return "", errors.New("Notion 时区无效")
	}
	if _, err := exports.ValidateDay(day.Date, zone); err != nil {
		return "", err
	}
	find := func() (map[string]any, error) {
		rows, err := s.query(ctx, s.Settings.DailySource, map[string]any{
			"property": "日期", "date": map[string]any{"equals": day.Date},
		})
		if err != nil {
			return nil, err
		}
		if len(rows) > 1 {
			return nil, &Error{Code: "duplicate"}
		}
		if len(rows) == 1 {
			return rows[0], nil
		}
		return nil, nil
	}
	generated := renderDay(day)
	page, err := s.createOnce(ctx, s.syncKey("day", day.Date, day.Timezone), "/pages", map[string]any{
		"parent":     map[string]any{"type": "data_source_id", "data_source_id": s.Settings.DailySource},
		"properties": dayProperties(day, "待补齐"),
		"markdown":   generated + "\n\n" + notesHeading + "\n\n可在这里添加个人备注；Daylog 更新上面的活动记录时会保留本区域。",
	}, find)
	if err != nil {
		return "", err
	}
	pageID := str(page["id"])
	if pageID == "" || page["in_trash"] == true || page["is_archived"] == true {
		return "", errors.New("Notion 日志页面已移入回收站或不可用，请恢复后重试")
	}
	// Until the complete body is confirmed, the page must remain provisional.
	if _, err = s.Client.request(ctx, "PATCH", "/pages/"+pageID, map[string]any{"properties": dayProperties(day, "待补齐")}); err != nil {
		return "", err
	}
	if err = s.updateContent(ctx, pageID, generated); err != nil {
		return "", err
	}
	status := "已整理"
	if day.Ongoing || day.PendingBatches > 0 || day.FailedBatches > 0 || day.DraftFrames > 0 {
		status = "待补齐"
	}
	final, err := s.Client.request(ctx, "PATCH", "/pages/"+pageID, map[string]any{"properties": dayProperties(day, status)})
	if err != nil {
		return "", err
	}
	url := str(final["url"])
	if url == "" {
		url = "https://www.notion.so/" + strings.ReplaceAll(pageID, "-", "")
	}
	return url, nil
}
