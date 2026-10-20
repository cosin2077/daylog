package notion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"daylog/internal/exports"
)

func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *Service) baseProperties(key, hash string) map[string]any {
	return map[string]any{"Daylog ID": textProperty(key), "来源": textProperty(s.Settings.SourceID), "内容指纹": textProperty(hash)}
}
func title(text string) map[string]any { return map[string]any{"title": rich(text)} }
func date(text string) map[string]any  { return map[string]any{"date": map[string]any{"start": text}} }
func selectValue(text string) map[string]any {
	if text == "" {
		return map[string]any{"select": nil}
	}
	return map[string]any{"select": map[string]any{"name": text}}
}
func (s *Service) upsert(ctx context.Context, source, key, hash string, properties map[string]any, existing map[string]any, children []any) (map[string]any, error) {
	if existing != nil {
		if propertyText(existing, "内容指纹") == hash {
			return existing, nil
		}
		// User page bodies are never patched: human notes remain independent.
		return s.Client.request(ctx, "PATCH", "/pages/"+str(existing["id"]), map[string]any{"properties": properties})
	}
	find := func() (map[string]any, error) {
		rows, err := s.query(ctx, source, matchText("Daylog ID", key))
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
	body := map[string]any{"parent": map[string]any{"type": "data_source_id", "data_source_id": source}, "properties": properties}
	if len(children) > 0 {
		body["children"] = children
	}
	page, err := s.createOnce(ctx, key, "/pages", body, find)
	if err != nil {
		return nil, err
	}
	if propertyText(page, "内容指纹") != hash {
		return s.Client.request(ctx, "PATCH", "/pages/"+str(page["id"]), map[string]any{"properties": properties})
	}
	return page, nil
}
func paragraph(text string) any {
	return map[string]any{"object": "block", "type": "paragraph", "paragraph": map[string]any{"rich_text": rich(text)}}
}
func (s *Service) Deliver(ctx context.Context, day exports.Day) (string, error) {
	key := s.syncKey("day", day.Date, day.Timezone)
	rows, err := s.query(ctx, s.Settings.DailySource, matchText("Daylog ID", key))
	if err != nil {
		return "", err
	}
	if len(rows) > 1 {
		return "", &Error{Code: "duplicate"}
	}
	var existing map[string]any
	if len(rows) == 1 {
		existing = rows[0]
	}
	properties := s.baseProperties(key, day.Hash())
	properties["名称"] = title(day.Date + " · 日报")
	properties["日期"] = date(day.Date)
	properties["时区"] = textProperty(day.Timezone)
	properties["估计秒数"] = numberProperty(day.EstimatedSeconds)
	properties["邻接推断秒数"] = numberProperty(day.InferredSeconds)
	properties["关键帧插值秒数"] = numberProperty(day.InterpolatedSeconds)
	properties["未估时秒数"] = numberProperty(day.UnknownSeconds)
	properties["旧版覆盖秒数"] = numberProperty(day.LegacyCoverageSeconds)
	properties["活动数"] = numberProperty(int64(len(day.Activities)))
	properties["已整理段数"] = numberProperty(int64(day.AnalyzedBatches))
	properties["待整理段数"] = numberProperty(int64(day.PendingBatches))
	properties["失败段数"] = numberProperty(int64(day.FailedBatches))
	properties["未封批截图数"] = numberProperty(int64(day.DraftFrames))
	status := "已同步"
	if day.PendingBatches > 0 || day.DraftFrames > 0 {
		status = "整理中"
	}
	properties["状态"] = selectValue("同步中")
	properties["简要记录"] = textProperty(fmt.Sprintf("已整理 %d 段记录，包含 %d 个活动片段。时间均为观察估计；空白或未估时不等于休息。详细描述与估时区间见 Daylog 活动数据库。", day.AnalyzedBatches, len(day.Activities)))
	// Only mark the daily summary complete after every activity has been written.
	daily, err := s.upsert(ctx, s.Settings.DailySource, key, day.Hash()+":writing", properties, existing, []any{paragraph("本页属性由 Daylog 同步。每个活动的描述、动作类别、估时来源及不确定性保存在关联的 Daylog 活动数据库；不上传截图。"), map[string]any{"object": "block", "type": "heading_2", "heading_2": map[string]any{"rich_text": rich("我的复盘")}}, paragraph("在这里记录你的想法、决定和下一步；Daylog 不会修改页面正文。")})
	if err != nil {
		return "", err
	}
	dailyID := str(daily["id"])
	activities, err := s.query(ctx, s.Settings.ActivitySource, matchText("日期标识", key))
	if err != nil {
		return "", err
	}
	index, err := uniqueRows(activities)
	if err != nil {
		return "", err
	}
	for _, a := range day.Activities {
		id := s.syncKey("activity", a.ID, day.Timezone)
		hash := digest(a)
		p := s.baseProperties(id, hash)
		p["名称"] = title(a.Title)
		p["日期"] = date(day.Date)
		p["日期标识"] = textProperty(key)
		p["时区"] = textProperty(day.Timezone)
		p["任务 ID"] = textProperty(a.TaskID)
		p["任务"] = textProperty(a.Task)
		p["类别"] = selectValue(a.Category)
		p["描述"] = textProperty(a.Summary)
		p["动作"] = textProperty(strings.Join(a.Actions, "\n"))
		p["不确定性"] = textProperty(strings.Join(a.Uncertainties, "\n"))
		p["置信度"] = selectValue(a.Confidence)
		p["应用"] = textProperty(strings.Join(a.Apps, "、"))
		p["来源渠道"] = textProperty(a.Source)
		p["模型"] = textProperty(a.Model)
		p["提示版本"] = textProperty(a.PromptVersion)
		p["证据图数"] = numberProperty(int64(a.EvidenceCount))
		durationStatus := "未估时"
		if a.EstimatedSeconds > 0 {
			durationStatus = "已估计"
		}
		if a.DurationMethod == "legacy_model_range" {
			durationStatus = "旧版范围"
		}
		p["时长状态"] = selectValue(durationStatus)
		from, until := a.Start.Truncate(time.Millisecond), a.End.Truncate(time.Millisecond)
		when := map[string]any{"start": from.Format(time.RFC3339Nano)}
		if until.After(from) {
			when["end"] = until.Format(time.RFC3339Nano)
		}
		p["时间"] = map[string]any{"date": when}
		p["估计秒数"] = numberProperty(a.EstimatedSeconds)
		p["邻接推断秒数"] = numberProperty(a.InferredSeconds)
		p["关键帧插值秒数"] = numberProperty(a.InterpolatedSeconds)
		p["日志"] = map[string]any{"relation": []any{map[string]any{"id": dailyID}}}
		raw, _ := json.Marshal(a.Spans)
		p["估时区间"] = textProperty(string(raw))
		if _, err = s.upsert(ctx, s.Settings.ActivitySource, id, hash, p, index[id], nil); err != nil {
			return "", err
		}
	}
	properties["状态"] = selectValue(status)
	properties["最后同步"] = date(time.Now().UTC().Format(time.RFC3339))
	properties["内容指纹"] = textProperty(day.Hash())
	final, err := s.Client.request(ctx, "PATCH", "/pages/"+dailyID, map[string]any{"properties": properties})
	if err != nil {
		return "", err
	}
	url := str(final["url"])
	if url == "" {
		url = "https://www.notion.so/" + strings.ReplaceAll(dailyID, "-", "")
	}
	return url, nil
}
