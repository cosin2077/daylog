package notion

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"daylog/internal/exports"
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
	return map[string]any{
		"名称": map[string]any{"title": map[string]any{}},
		"日期": map[string]any{"date": map[string]any{}},
		"状态": map[string]any{"select": map[string]any{"options": []any{
			map[string]any{"name": "已整理", "color": "green"},
			map[string]any{"name": "待补齐", "color": "yellow"},
		}}},
	}
}

func (s *Service) databaseMarker() string {
	return "Daylog source=" + s.Settings.SourceID + " layout=" + DailyLayout
}

// Names alone imply no ownership. An existing compatible database must be
// selected explicitly with --database; fresh setups only adopt their marker.
func (s *Service) ensureDatabase(ctx context.Context) (map[string]any, error) {
	find := func() (map[string]any, error) {
		blocks, err := s.Client.children(ctx, s.Settings.ParentPage)
		if err != nil {
			return nil, err
		}
		var match map[string]any
		for _, block := range blocks {
			if str(block["type"]) != "child_database" {
				continue
			}
			db, err := s.Client.request(ctx, "GET", "/databases/"+str(block["id"]), nil)
			if err != nil {
				return nil, err
			}
			if db["in_trash"] == true || plain(db["description"]) != s.databaseMarker() {
				continue
			}
			if match != nil {
				return nil, &Error{Code: "duplicate"}
			}
			match = db
		}
		return match, nil
	}
	return s.createOnce(ctx, "database:daily-page", "/databases", map[string]any{
		"parent": map[string]any{"type": "page_id", "page_id": s.Settings.ParentPage},
		"title":  rich("Daylog"), "description": rich(s.databaseMarker()), "is_inline": false,
		"initial_data_source": map[string]any{"properties": dailySchema()},
	}, find)
}

func (s *Service) Setup(ctx context.Context) error {
	if s.Settings.Layout != DailyLayout {
		return errors.New("Notion 仍为旧版双库配置，请重新运行 daylog notion setup")
	}
	if err := s.Client.CheckParent(ctx, s.Settings.ParentPage); err != nil {
		return err
	}
	var db map[string]any
	var err error
	if s.Settings.DailyDatabase == "" {
		db, err = s.ensureDatabase(ctx)
	} else {
		db, err = s.Client.request(ctx, "GET", "/databases/"+s.Settings.DailyDatabase, nil)
	}
	if err != nil {
		return err
	}
	if db["in_trash"] == true || !sameID(str(obj(db["parent"])["page_id"]), s.Settings.ParentPage) {
		return errors.New("Notion 数据库不属于目标页面或已移入回收站，请检查 --database")
	}
	sources := items(db["data_sources"])
	if len(sources) != 1 || str(obj(sources[0])["id"]) == "" {
		return errors.New("Daylog 数据库的数据源不唯一，请检查目标数据库")
	}
	s.Settings.DailyDatabase = str(db["id"])
	s.Settings.DailySource = str(obj(sources[0])["id"])
	s.Settings.ActivityDatabase, s.Settings.ActivitySource = "", ""
	if err = SaveSettings(s.Root, s.Settings); err != nil {
		return err
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
	if s.Settings.Layout != DailyLayout {
		return errors.New("Notion 仍为旧版双库配置，请重新运行 daylog notion setup；已有单页库可用 --database 指定")
	}
	if s.Settings.DailySource == "" {
		return errors.New("Notion 数据库尚未配置，请运行 daylog notion setup")
	}
	data, err := s.Client.request(ctx, "GET", "/data_sources/"+s.Settings.DailySource, nil)
	if err != nil {
		return err
	}
	if data["in_trash"] == true {
		return errors.New("Notion 数据库已移入回收站，请恢复后重试")
	}
	properties := obj(data["properties"])
	for name, schema := range dailySchema() {
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
		if str(obj(r["request_status"])["type"]) == "incomplete" {
			return nil, errors.New("Notion 查询结果不完整，已停止同步以避免重复或遗漏")
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

func (s *Service) syncKey(parts ...string) string {
	return "daylog:" + s.Settings.SourceID + ":" + strings.Join(parts, ":")
}
func sameID(a, b string) bool {
	return a != "" && strings.EqualFold(strings.ReplaceAll(a, "-", ""), strings.ReplaceAll(b, "-", ""))
}
func (c *Client) CheckParent(ctx context.Context, id string) error {
	normalized, err := ParsePage(id)
	if err != nil {
		return err
	}
	page, err := c.request(ctx, "GET", "/pages/"+normalized, nil)
	if err != nil {
		return err
	}
	if page["in_trash"] == true || page["is_archived"] == true || page["archived"] == true {
		return errors.New("Notion 目标页面已归档或移入回收站，请恢复后重试")
	}
	return nil
}
