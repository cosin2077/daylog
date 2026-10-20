package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const apiVersion = "2026-03-11"

type Client struct {
	token, base string
	http        *http.Client
	interval    time.Duration
	last        time.Time
}

func NewClient(token string) *Client {
	return &Client{token: token, base: "https://api.notion.com/v1", http: &http.Client{Timeout: 25 * time.Second}, interval: 350 * time.Millisecond}
}

type Error struct {
	Status      int
	Code        string
	Wait        time.Duration
	Uncertain   bool
	CommittedID string
}

func (e *Error) Error() string {
	switch e.Code {
	case "uncertain_write":
		return "Notion 写入结果尚未确认，稍后将自动核验；若确认远端不存在，可用 --retry-uncertain 重试"
	case "duplicate":
		return "Notion 中存在重复的 Daylog 标识，请先检查重复记录"
	case "block_limit":
		return "Notion 免费工作区区块额度已用尽；请检查工作区套餐与成员数量"
	case "missing_token":
		return "未找到 Notion 凭证，请运行 daylog notion setup"
	}
	switch e.Status {
	case 400:
		return "Notion 请求或数据库字段无效，请运行 daylog notion check 检查配置"
	case 401:
		return "Notion Token 无效或已过期，请重新运行 daylog notion setup"
	case 403, 404:
		return "Notion 页面或数据库不可访问，请检查页面连接与读写权限"
	case 429, 529:
		return "Notion 暂时限流或繁忙，将按 Retry-After 自动补同步"
	}
	return "Notion 连接或服务暂不可用，将稍后补同步"
}
func (e *Error) RetryDelay() time.Duration {
	if e.Wait > 0 {
		return e.Wait
	}
	if e.Status == 429 || e.Status == 529 {
		return time.Minute
	}
	return 0
}
func (e *Error) Permanent() bool {
	return e.Status == 400 || e.Status == 401 || e.Status == 403 || e.Status == 404 || e.Code == "duplicate"
}

func (c *Client) request(ctx context.Context, method, path string, body any) (map[string]any, error) {
	if c.token == "" {
		return nil, &Error{Code: "missing_token", Wait: time.Minute}
	}
	delay := c.interval - time.Since(c.last)
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	var input io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if len(raw) > 480000 {
			return nil, &Error{Status: 400}
		}
		input = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", apiVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Daylog")
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("Notion redirects are refused") }
	c.last = time.Now()
	response, err := client.Do(req)
	write := method != "GET" && !strings.HasSuffix(path, "/query")
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var op *net.OpError
		uncertain := write
		if errors.As(err, &op) && op.Op == "dial" {
			uncertain = false
		}
		return nil, &Error{Uncertain: uncertain}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, &Error{Status: response.StatusCode, Uncertain: write}
	}
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil {
		return nil, &Error{Status: response.StatusCode, Uncertain: write}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		wait, _ := strconv.Atoi(response.Header.Get("Retry-After"))
		code, _ := result["code"].(string)
		additional, _ := result["additional_data"].(map[string]any)
		committed, _ := additional["committed_resource_id"].(string)
		if additional["block_limit"] != nil {
			code = "block_limit"
		}
		uncertain := write && response.StatusCode >= 500 && response.StatusCode != 529
		return nil, &Error{Status: response.StatusCode, Code: code, Wait: time.Duration(max(wait, 0)) * time.Second, Uncertain: uncertain, CommittedID: committed}
	}
	return result, nil
}
func str(v any) string         { s, _ := v.(string); return s }
func items(v any) []any        { s, _ := v.([]any); return s }
func obj(v any) map[string]any { s, _ := v.(map[string]any); return s }
func rich(text string) []any {
	result := []any{}
	r := []rune(text)
	for len(r) > 0 {
		n := min(len(r), 1800)
		result = append(result, map[string]any{"type": "text", "text": map[string]any{"content": string(r[:n])}})
		r = r[n:]
	}
	return result
}
func textProperty(text string) map[string]any { return map[string]any{"rich_text": rich(text)} }
func numberProperty(n int64) map[string]any   { return map[string]any{"number": n} }
func plain(v any) string {
	var b strings.Builder
	for _, item := range items(v) {
		m := obj(item)
		s := str(m["plain_text"])
		if s == "" {
			s = str(obj(m["text"])["content"])
		}
		b.WriteString(s)
	}
	return b.String()
}
func propertyText(page map[string]any, key string) string {
	return plain(obj(obj(page["properties"])[key])["rich_text"])
}
