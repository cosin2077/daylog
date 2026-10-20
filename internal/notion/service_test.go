package notion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"daylog/internal/exports"
)

type fakeAPI struct {
	mu                       sync.Mutex
	next                     int
	databases                map[string]map[string]any
	sources                  map[string]map[string]any
	pages                    map[string]map[string]any
	children                 map[string][]any
	posts, patches, requests int
	dropNext, quota          bool
}

func fakeClient(t *testing.T) (*Client, *fakeAPI) {
	t.Helper()
	f := &fakeAPI{databases: map[string]map[string]any{}, sources: map[string]map[string]any{}, pages: map[string]map[string]any{}, children: map[string][]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++
		if r.Header.Get("Authorization") != "Bearer fixture-not-real" || r.Header.Get("Notion-Version") != apiVersion {
			t.Error("bad authentication/version header")
		}
		send := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
		var body map[string]any
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&body)
		}
		newID := func() string { f.next++; return fmt.Sprintf("%032x", f.next) }
		path := r.URL.Path
		switch {
		case path == "/pages/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" && r.Method == "GET":
			send(map[string]any{"id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
		case strings.HasPrefix(path, "/blocks/") && strings.HasSuffix(path, "/children"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/blocks/"), "/children")
			send(map[string]any{"results": f.children[id], "has_more": false})
		case path == "/databases" && r.Method == "POST":
			id, source := newID(), newID()
			db := map[string]any{"id": id, "description": body["description"], "data_sources": []any{map[string]any{"id": source}}}
			f.databases[id] = db
			props := map[string]any{}
			for name, v := range obj(obj(body["initial_data_source"])["properties"]) {
				p := obj(v)
				kind := ""
				for k := range p {
					kind = k
				}
				props[name] = map[string]any{"type": kind}
			}
			f.sources[source] = map[string]any{"id": source, "properties": props}
			parent := str(obj(body["parent"])["page_id"])
			f.children[parent] = append(f.children[parent], map[string]any{"id": id, "type": "child_database"})
			send(db)
		case strings.HasPrefix(path, "/databases/") && r.Method == "GET":
			send(f.databases[strings.TrimPrefix(path, "/databases/")])
		case strings.HasPrefix(path, "/data_sources/") && r.Method == "GET":
			send(f.sources[strings.TrimPrefix(path, "/data_sources/")])
		case strings.HasPrefix(path, "/data_sources/") && strings.HasSuffix(path, "/query"):
			source := strings.TrimSuffix(strings.TrimPrefix(path, "/data_sources/"), "/query")
			filter := obj(body["filter"])
			property := str(filter["property"])
			value := str(obj(filter["rich_text"])["equals"])
			var ids []string
			for id, page := range f.pages {
				if str(obj(page["parent"])["data_source_id"]) == source && propertyText(page, property) == value {
					ids = append(ids, id)
				}
			}
			sort.Strings(ids)
			offset, _ := strconv.Atoi(str(body["start_cursor"]))
			end := min(offset+100, len(ids))
			result := []any{}
			for _, id := range ids[offset:end] {
				result = append(result, f.pages[id])
			}
			var cursor any
			if end < len(ids) {
				cursor = strconv.Itoa(end)
			}
			send(map[string]any{"results": result, "has_more": end < len(ids), "next_cursor": cursor})
		case path == "/pages" && r.Method == "POST":
			if f.quota {
				w.WriteHeader(403)
				send(map[string]any{"code": "restricted_resource", "additional_data": map[string]any{"block_limit": "block_creation"}})
				return
			}
			id := newID()
			page := map[string]any{"id": id, "url": "https://notion.so/" + id, "properties": body["properties"], "parent": body["parent"]}
			f.pages[id] = page
			f.children[id] = items(body["children"])
			f.posts++
			if f.dropNext {
				f.dropNext = false
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			send(page)
		case strings.HasPrefix(path, "/pages/") && r.Method == "GET":
			send(f.pages[strings.TrimPrefix(path, "/pages/")])
		case strings.HasPrefix(path, "/pages/") && r.Method == "PATCH":
			if body["children"] != nil || body["erase_content"] != nil || body["in_trash"] != nil {
				t.Error("user body or pages overwritten")
			}
			id := strings.TrimPrefix(path, "/pages/")
			page := f.pages[id]
			if page == nil {
				t.Error("patch unknown page")
				w.WriteHeader(404)
				send(map[string]any{})
				return
			}
			for key, v := range obj(body["properties"]) {
				obj(page["properties"])[key] = v
			}
			f.patches++
			send(page)
		default:
			t.Errorf("unexpected route %s %s", r.Method, path)
			w.WriteHeader(404)
			send(map[string]any{"code": "not_found"})
		}
	}))
	t.Cleanup(server.Close)
	client := NewClient("fixture-not-real")
	client.base = server.URL
	client.http = server.Client()
	client.interval = 0
	return client, f
}
func setupFake(t *testing.T) (*Service, *fakeAPI) {
	t.Helper()
	c, f := fakeClient(t)
	settings, err := NewSettings("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Asia/Shanghai", "2026-10-18")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Root: t.TempDir(), Settings: settings, Client: c}
	if err = s.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, f
}
func sampleDay(n int) exports.Day {
	zone, _ := time.LoadLocation("Asia/Shanghai")
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, zone)
	d := exports.Day{SchemaVersion: 1, Date: "2026-10-18", Timezone: zone.String(), AnalyzedBatches: 1, EstimatedSeconds: 60, SourceBatches: []string{"batch"}}
	for i := 0; i < n; i++ {
		d.Activities = append(d.Activities, exports.Activity{ID: fmt.Sprint("activity-", i), Title: "查阅文档", Task: "缓存任务", Category: "查资料", Summary: "阅读接口文档；没有执行测试的证据。", Confidence: "medium", Start: at.Add(time.Duration(i) * time.Minute), End: at.Add(time.Duration(i)*time.Minute + 30*time.Second), EstimatedSeconds: 30, Actions: []string{"阅读"}})
	}
	return d
}

func TestSetupIdempotentAndCompleteQuantitativeSync(t *testing.T) {
	s, f := setupFake(t)
	if err := s.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.databases) != 2 {
		t.Fatal("duplicate setup", len(f.databases))
	}
	day := sampleDay(121)
	day.PendingBatches = 1
	url, err := s.Deliver(context.Background(), day)
	if err != nil || url == "" {
		t.Fatal(url, err)
	}
	if f.posts != 122 {
		t.Fatal("truncated activities", f.posts)
	}
	var daily map[string]any
	for _, page := range f.pages {
		if strings.Contains(propertyText(page, "Daylog ID"), ":day:") {
			daily = page
		}
	}
	if plain(obj(obj(daily["properties"])["名称"])["title"]) != "2026-10-18 · 日报" {
		t.Fatal("execution date used as title")
	}

	if str(obj(obj(obj(daily["properties"])["状态"])["select"])["name"]) != "整理中" {
		t.Fatal("incomplete day marked final")
	}
	id := str(daily["id"])
	f.children[id] = append(f.children[id], paragraph("用户的人工复盘"))
	before := len(f.children[id])
	posts := f.posts
	if _, err = s.Deliver(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	if f.posts != posts || len(f.children[id]) != before {
		t.Fatal("duplicate rows or user notes changed")
	}
	day.PendingBatches = 0
	day.Activities[0].Summary = "补充辨认出的事实"
	if _, err = s.Deliver(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	if f.posts != posts || str(obj(obj(obj(daily["properties"])["状态"])["select"])["name"]) != "已同步" {
		t.Fatal("late analysis created duplicate or not finalized")
	}
	raw, _ := os.ReadFile(s.receiptFile(s.syncKey("day", day.Date, day.Timezone)))
	if strings.Contains(string(raw), "fixture-not-real") {
		t.Fatal("token persisted")
	}
}
func TestCommittedCreateWithLostResponseReconciles(t *testing.T) {
	s, f := setupFake(t)
	f.mu.Lock()
	f.dropNext = true
	f.mu.Unlock()
	day := sampleDay(1)
	if _, err := s.Deliver(context.Background(), day); err == nil {
		t.Fatal("lost response falsely succeeded")
	}
	if f.postCount() != 1 {
		t.Fatal(f.postCount())
	}
	if _, err := s.Deliver(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	if f.postCount() != 2 {
		t.Fatal("duplicate day after lost response", f.postCount())
	}
}
func TestQuotaIsPermanentAndDoesNotLeakToken(t *testing.T) {
	s, f := setupFake(t)
	f.mu.Lock()
	f.quota = true
	f.mu.Unlock()
	_, err := s.Deliver(context.Background(), sampleDay(1))
	api, ok := err.(*Error)
	if !ok || !api.Permanent() || !strings.Contains(err.Error(), "区块额度") || strings.Contains(err.Error(), s.Client.token) {
		t.Fatal(err)
	}
}
func TestUncertainWriteNeverBlindlyRepeats(t *testing.T) {
	s, f := setupFake(t)
	f.mu.Lock()
	f.dropNext = true
	f.mu.Unlock()
	day := sampleDay(1)
	s.Deliver(context.Background(), day)
	f.mu.Lock()
	for id := range f.pages {
		delete(f.pages, id)
	}
	f.mu.Unlock()
	_, err := s.Deliver(context.Background(), day)
	if err == nil || !strings.Contains(err.Error(), "尚未确认") || f.postCount() != 1 {
		t.Fatal("uncertain create repeated", err, f.postCount())
	}
}
func TestLimitsAndRichTextAndPageParsing(t *testing.T) {
	text := strings.Repeat("字", 4200)
	chunks := rich(text)
	if len(chunks) != 3 || plain(chunks) != text {
		t.Fatal("text truncated")
	}
	for _, raw := range []string{"https://app.notion.com/p/daylog-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "https://www.notion.so/Daylog-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa?source=copy_link", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"} {
		id, err := ParsePage(raw)
		if err != nil || id != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			t.Fatal(id, err)
		}
	}
	for _, raw := range []string{"https://evil.example/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "http://notion.so/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "../aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if _, err := ParsePage(raw); err == nil {
			t.Fatal("invalid parent accepted")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7200")
		w.WriteHeader(429)
		json.NewEncoder(w).Encode(map[string]any{"code": "rate_limited"})
	}))
	defer server.Close()
	c := NewClient("fixture-not-real")
	c.base = server.URL
	c.interval = 0
	_, err := c.request(context.Background(), "GET", "/pages/a", nil)
	api, ok := err.(*Error)
	if !ok || api.RetryDelay() != 2*time.Hour || api.Permanent() {
		t.Fatal(err)
	}
}
func TestRedirectDoesNotForwardAuthorization(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c := NewClient("fixture-not-real")
	c.base = server.URL
	c.interval = 0
	c.request(context.Background(), "GET", "/page", nil)
	if calls != 0 {
		t.Fatal("redirect forwarded token")
	}
}

func (f *fakeAPI) postCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.posts }

func TestSuccessfulReceiptUsesKnownIDWhenQueryMisses(t *testing.T) {
	s, f := setupFake(t)
	key := s.syncKey("day", "2026-10-18", "Asia/Shanghai")
	body := map[string]any{"parent": map[string]any{"data_source_id": s.Settings.DailySource}, "properties": s.baseProperties(key, "hash")}
	find := func() (map[string]any, error) { return nil, nil }
	first, err := s.createOnce(context.Background(), key, "/pages", body, find)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.createOnce(context.Background(), key, "/pages", body, find)
	if err != nil || str(first["id"]) != str(second["id"]) || f.postCount() != 1 {
		t.Fatal("query miss duplicated confirmed create", err)
	}
}
func TestCancelledBeforeSendDoesNotPoisonReceipt(t *testing.T) {
	s, f := setupFake(t)
	s.Client.last = time.Now()
	s.Client.interval = time.Hour
	key := s.syncKey("day", "2026-10-18", "Asia/Shanghai")
	body := map[string]any{"properties": s.baseProperties(key, "hash")}
	find := func() (map[string]any, error) { return nil, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := s.createOnce(ctx, key, "/pages", body, find)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	records, err := readReceipts(s.receiptFile(key))
	if err != nil || records[key].Pending || f.postCount() != 0 {
		t.Fatal("unsent request became uncertain", err)
	}
	s.Client.interval = 0
	if _, err = s.createOnce(context.Background(), key, "/pages", body, find); err != nil {
		t.Fatal("unsent request could not retry", err)
	}
}
func TestReceiptBucketsSurviveLifetimeScale(t *testing.T) {
	s, _ := setupFake(t)
	records := map[string]receipt{}
	for i := 0; i < 12000; i++ {
		records[fmt.Sprint(i)+strings.Repeat("x", 100)+":2026-10-18"] = receipt{ID: fmt.Sprintf("%032x", i+1)}
	}
	path := s.receiptFile("daylog:source:day:2026-10-18")
	if err := writeReceipts(path, records); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Size() <= 2<<20 {
		t.Fatal("scale fixture too small")
	}
	loaded, err := readReceipts(path)
	if err != nil || len(loaded) != 12000 {
		t.Fatal("lifetime receipt read failed", err, len(loaded))
	}
	if s.receiptFile("daylog:source:day:2026-10-19") == path {
		t.Fatal("all dates still share lifetime file")
	}
}
func TestLocalTimezoneCannotBePersisted(t *testing.T) {
	if _, err := NewSettings("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Local", "2026-10-18"); err == nil {
		t.Fatal("mutable timezone accepted")
	}
	if _, err := NewSettings("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "America/Los_Angeles", "2026-10-18"); err != nil {
		t.Fatal("bundled IANA timezone unavailable", err)
	}
}
