package notion

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"daylog/internal/journal"
)

type Settings struct {
	SyncTimes        []string `json:"sync_times,omitempty"`
	Layout           string   `json:"layout,omitempty"`
	Enabled          bool     `json:"enabled"`
	SourceID         string   `json:"source_id"`
	ParentPage       string   `json:"parent_page"`
	DailyDatabase    string   `json:"daily_database,omitempty"`
	DailySource      string   `json:"daily_source,omitempty"`
	ActivityDatabase string   `json:"activity_database,omitempty"`
	ActivitySource   string   `json:"activity_source,omitempty"`
	Timezone         string   `json:"timezone"`
	Since            string   `json:"since"`
}

const DailyLayout = "daily_page_v1"

func settingsPath(root string) string { return filepath.Join(root, "integrations", "notion.json") }
func (s Settings) StateRoot(root string) string {
	path := filepath.Join(root, "integrations", "notion", s.SourceID)
	if s.Layout == DailyLayout {
		path = filepath.Join(path, DailyLayout)
		if s.DailyDatabase != "" {
			path = filepath.Join(path, strings.ToLower(strings.ReplaceAll(s.DailyDatabase, "-", "")))
		}
	}
	return path
}
func (s Settings) SecretName() string { return "notion." + s.SourceID }
func ReadSettings(root string) (Settings, error) {
	var s Settings
	err := journal.ReadJSON(settingsPath(root), &s)
	if err != nil {
		return s, err
	}
	if !sourceID.MatchString(s.SourceID) {
		return s, errors.New("Notion 配置中的来源标识无效")
	}
	if _, err = ParsePage(s.ParentPage); err != nil {
		return s, err
	}
	if s.Timezone == "" || s.Timezone == "Local" {
		return s, errors.New("请配置固定 IANA 时区，例如 Asia/Shanghai")
	}
	if _, err = time.LoadLocation(s.Timezone); err != nil {
		return s, errors.New("Notion 时区无效")
	}
	if _, err = time.Parse("2006-01-02", s.Since); err != nil {
		return s, errors.New("Notion 开始日期无效")
	}
	if _, err = s.Checkpoints(); err != nil {
		return s, err
	}
	for _, id := range []string{s.DailyDatabase, s.DailySource, s.ActivityDatabase, s.ActivitySource} {
		if id != "" {
			if _, err = ParsePage(id); err != nil {
				return s, errors.New("Notion 数据库标识无效")
			}
		}
	}
	return s, nil
}
func SaveSettings(root string, s Settings) error {
	if err := os.MkdirAll(filepath.Dir(settingsPath(root)), 0700); err != nil {
		return err
	}
	return journal.WriteJSON(settingsPath(root), s)
}

// Civil clock minutes keep daily checkpoints stable on 23/25-hour DST days.
func (s Settings) Checkpoints() ([]int, error) {
	seen := map[int]bool{}
	var result []int
	for _, raw := range s.SyncTimes {
		at, err := time.Parse("15:04", raw)
		if err != nil || at.Format("15:04") != raw {
			return nil, errors.New("Notion 同步时间须为 HH:MM，例如 12:00、16:00")
		}
		minute := at.Hour()*60 + at.Minute()
		if !seen[minute] {
			seen[minute] = true
			result = append(result, minute)
		}
	}
	sort.Ints(result)
	return result, nil
}

func ParseSyncTimes(raw string) ([]string, error) {
	settings := Settings{}
	if strings.TrimSpace(raw) != "" {
		for _, part := range strings.Split(raw, ",") {
			settings.SyncTimes = append(settings.SyncTimes, strings.TrimSpace(part))
		}
	}
	minutes, err := settings.Checkpoints()
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, minute := range minutes {
		result = append(result, fmt.Sprintf("%02d:%02d", minute/60, minute%60))
	}
	return result, nil
}

var sourceID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var pageID = regexp.MustCompile(`(?i)[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}|[a-f0-9]{32}`)

func ParsePage(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	part := raw
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.User != nil {
			return "", errors.New("请提供 HTTPS Notion 页面链接或页面 ID")
		}
		host := strings.ToLower(u.Hostname())
		if !(host == "notion.so" || host == "www.notion.so" || host == "app.notion.com" || host == "www.notion.com" || host == "notion.com" || host == "notion.site" || strings.HasSuffix(host, ".notion.site")) {
			return "", errors.New("请提供 Notion 页面链接")
		}
		part = u.Path
	}
	matches := pageID.FindAllString(part, -1)
	if len(matches) == 0 {
		return "", errors.New("页面链接中没有有效的 Notion 页面 ID")
	}
	id := strings.ToLower(strings.ReplaceAll(matches[len(matches)-1], "-", ""))
	if !strings.Contains(raw, "://") && !strings.EqualFold(strings.ReplaceAll(raw, "-", ""), id) {
		return "", errors.New("Notion 页面 ID 无效")
	}
	return id, nil
}
func NewSettings(parent, zone, since string) (Settings, error) {
	id, err := ParsePage(parent)
	if err != nil {
		return Settings{}, err
	}
	if zone == "" || zone == "Local" {
		return Settings{}, errors.New("请用 --timezone 指定固定 IANA 时区，例如 Asia/Shanghai")
	}
	if _, err = time.LoadLocation(zone); err != nil {
		return Settings{}, errors.New("时区须为有效的 IANA 名称，例如 Asia/Shanghai")
	}
	if _, err = time.ParseInLocation("2006-01-02", since, time.UTC); err != nil {
		return Settings{}, errors.New("开始日期须为 YYYY-MM-DD")
	}
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return Settings{}, err
	}
	return Settings{Layout: DailyLayout, SourceID: hex.EncodeToString(b), ParentPage: id, Timezone: zone, Since: since}, nil
}
func SystemTimezone() string {
	if name := time.Local.String(); name != "Local" {
		if _, err := time.LoadLocation(name); err == nil {
			return name
		}
	}
	if link, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(filepath.ToSlash(link), "zoneinfo/"); ok {
			if _, err = time.LoadLocation(name); err == nil {
				return name
			}
		}
	}
	return ""
}
