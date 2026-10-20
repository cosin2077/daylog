package notion

import (
	"context"
	"daylog/internal/exports"
	"errors"
	"fmt"
	"strings"
)

type SecretStore interface {
	Set(string, string) error
	Get(string) string
}
type Service struct {
	Root           string
	Settings       Settings
	Client         *Client
	AllowUncertain bool
}

func dailySchema() map[string]any {
	p := map[string]any{"名称": map[string]any{"title": map[string]any{}}, "日期": map[string]any{"date": map[string]any{}}, "状态": map[string]any{"select": map[string]any{}}, "最后同步": map[string]any{"date": map[string]any{}}}
	for _, name := range []string{"Daylog ID", "来源", "内容指纹", "时区", "简要记录"} {
		p[name] = map[string]any{"rich_text": map[string]any{}}
	}
	for _, name := range []string{"估计秒数", "邻接推断秒数", "关键帧插值秒数", "未估时秒数", "旧版覆盖秒数", "活动数", "已整理段数", "待整理段数", "失败段数"} {
		p[name] = map[string]any{"number": map[string]any{"format": "number"}}
	}
	return p
}
func activitySchema(daily string) map[string]any {
	p := map[string]any{"名称": map[string]any{"title": map[string]any{}}, "日期": map[string]any{"date": map[string]any{}}, "时间": map[string]any{"date": map[string]any{}}, "类别": map[string]any{"select": map[string]any{}}, "置信度": map[string]any{"select": map[string]any{}}, "时长状态": map[string]any{"select": map[string]any{}}, "日志": map[string]any{"relation": map[string]any{"data_source_id": daily, "type": "dual_property", "dual_property": map[string]any{}}}}
	for _, name := range []string{"Daylog ID", "来源", "内容指纹", "日期标识", "任务 ID", "任务", "描述", "动作", "不确定性", "估时区间", "时区", "应用", "来源渠道", "模型", "提示版本"} {
		p[name] = map[string]any{"rich_text": map[string]any{}}
	}
	for _, name := range []string{"估计秒数", "邻接推断秒数", "关键帧插值秒数", "证据图数"} {
		p[name] = map[string]any{"number": map[string]any{"format": "number"}}
	}
	return p
}

// Only adopt databases carrying our source marker; never repurpose a user's
// existing database just because its title happens to be "日志".
func (s *Service) ensureDatabase(ctx context.Context, role, title string, schema map[string]any) (string, string, error) {
	marker := "Daylog source=" + s.Settings.SourceID + " role=" + role
	find := func() (map[string]any, error) {
		cursor := ""
		for {
			path := "/blocks/" + s.Settings.ParentPage + "/children?page_size=100"
			if cursor != "" {
				path += "&start_cursor=" + cursor
			}
			result, err := s.Client.request(ctx, "GET", path, nil)
			if err != nil {
				return nil, err
			}
			for _, v := range items(result["results"]) {
				b := obj(v)
				if str(b["type"]) != "child_database" {
					continue
				}
				db, err := s.Client.request(ctx, "GET", "/databases/"+str(b["id"]), nil)
				if err != nil {
					return nil, err
				}
				if plain(db["description"]) == marker {
					return db, nil
				}
			}
			if result["has_more"] != true {
				return nil, nil
			}
			next := str(result["next_cursor"])
			if next == "" || next == cursor {
				return nil, errors.New("Notion 分页游标无效")
			}
			cursor = next
		}
	}
	db, err := s.createOnce(ctx, "database:"+role, "/databases", map[string]any{"parent": map[string]any{"type": "page_id", "page_id": s.Settings.ParentPage}, "title": rich(title), "description": rich(marker), "is_inline": false, "initial_data_source": map[string]any{"properties": schema}}, find)
	if err != nil {
		return "", "", err
	}
	id := str(db["id"])
	if len(items(db["data_sources"])) == 0 {
		db, err = s.Client.request(ctx, "GET", "/databases/"+id, nil)
		if err != nil {
			return "", "", err
		}
	}
	sources := items(db["data_sources"])
	if len(sources) != 1 {
		return "", "", errors.New("Daylog 数据库的数据源不唯一，请检查目标数据库")
	}
	source := str(obj(sources[0])["id"])
	if id == "" || source == "" {
		return "", "", errors.New("Notion 未返回数据库标识")
	}
	return id, source, nil
}
func (s *Service) Setup(ctx context.Context) error {
	if _, err := s.Client.request(ctx, "GET", "/pages/"+s.Settings.ParentPage, nil); err != nil {
		return err
	}
	var err error
	if s.Settings.DailySource == "" {
		s.Settings.DailyDatabase, s.Settings.DailySource, err = s.ensureDatabase(ctx, "daily", "Daylog 日志", dailySchema())
		if err != nil {
			return err
		}
		if err = SaveSettings(s.Root, s.Settings); err != nil {
			return err
		}
	}
	if s.Settings.ActivitySource == "" {
		s.Settings.ActivityDatabase, s.Settings.ActivitySource, err = s.ensureDatabase(ctx, "activities", "Daylog 活动", activitySchema(s.Settings.DailySource))
		if err != nil {
			return err
		}
		if err = SaveSettings(s.Root, s.Settings); err != nil {
			return err
		}
	}
	if err = s.Check(ctx); err != nil {
		return err
	}
	if err = exports.Resume(s.Settings.StateRoot(s.Root)); err != nil {
		return err
	}
	s.Settings.Enabled = true
	return SaveSettings(s.Root, s.Settings)
}
func (s *Service) Check(ctx context.Context) error {
	for source, expected := range map[string]map[string]any{s.Settings.DailySource: dailySchema(), s.Settings.ActivitySource: activitySchema(s.Settings.DailySource)} {
		if source == "" {
			return errors.New("Notion 数据库尚未配置，请运行 daylog notion setup")
		}
		data, err := s.Client.request(ctx, "GET", "/data_sources/"+source, nil)
		if err != nil {
			return err
		}
		properties := obj(data["properties"])
		for name, schema := range expected {
			actual := obj(properties[name])
			if actual == nil {
				return fmt.Errorf("Notion 缺少字段「%s」，请恢复字段名称后重试", name)
			}
			for kind := range obj(schema) {
				if str(actual["type"]) != kind {
					return fmt.Errorf("Notion 字段「%s」类型不匹配", name)
				}
			}
		}
	}
	return nil
}

func (s *Service) query(ctx context.Context, source string, filter map[string]any) ([]map[string]any, error) {
	var all []map[string]any
	cursor := ""
	for {
		body := map[string]any{"filter": filter, "page_size": 100}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		r, err := s.Client.request(ctx, "POST", "/data_sources/"+source+"/query", body)
		if err != nil {
			return nil, err
		}
		for _, v := range items(r["results"]) {
			all = append(all, obj(v))
		}
		if r["has_more"] != true {
			return all, nil
		}
		next := str(r["next_cursor"])
		if next == "" || next == cursor {
			return nil, errors.New("Notion 分页游标无效")
		}
		cursor = next
	}
}
func matchText(name, value string) map[string]any {
	return map[string]any{"property": name, "rich_text": map[string]any{"equals": value}}
}
func uniqueRows(rows []map[string]any) (map[string]map[string]any, error) {
	m := map[string]map[string]any{}
	for _, r := range rows {
		key := propertyText(r, "Daylog ID")
		if key == "" {
			continue
		}
		if m[key] != nil {
			return nil, &Error{Code: "duplicate"}
		}
		m[key] = r
	}
	return m, nil
}
func (s *Service) syncKey(parts ...string) string {
	return "daylog:" + s.Settings.SourceID + ":" + strings.Join(parts, ":")
}
