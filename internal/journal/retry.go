package journal

import (
	"context"
	"daylog/internal/native"
	"daylog/internal/platform"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var ErrDailyBudget = errors.New("daily analysis budget reached")
var ErrRetryCooling = errors.New("analysis retry is cooling down")

// One state vocabulary for CLI, live status and saved reports. An interrupted
// attempt is not a failed preparation round, including older queue records.
func PreparationState(b Batch) string {
	if strings.Contains(b.LastError, "canceled") {
		return "interrupted"
	}
	if b.LastError != "" {
		return "failed"
	}
	return "pending"
}

func RetryTime(b Batch) time.Time {
	if b.RetryAfter != nil {
		return *b.RetryAfter
	}
	// Older batches have no backoff field. Recover them without erasing attempts.
	if b.LastAttempt != nil {
		return b.LastAttempt.Add(15 * time.Minute)
	}
	return time.Time{}
}
func RetryReady(b Batch, now time.Time) bool { return !now.Before(RetryTime(b)) }

func retryDelay(rounds int) time.Duration {
	if rounds < 1 {
		rounds = 1
	}
	if rounds > 6 {
		rounds = 6
	}
	delay := 15 * time.Minute * time.Duration(1<<uint(rounds-1))
	if delay > 6*time.Hour {
		delay = 6 * time.Hour
	}
	return delay
}

func (s *Store) FinishFailure(id, reason string) error {
	return s.finishFailure(id, reason, func() error { return notifyAnalysisFailure(reason) })
}

func (s *Store) finishFailure(id, reason string, notify func() error) error {
	return s.locked(func() error {
		b, err := s.Batch(id)
		if err != nil {
			return err
		}
		b.FailureRounds++
		at := time.Now().UTC()
		next := at.Add(retryDelay(b.FailureRounds))
		b.LastAttempt = &at
		b.RetryAfter = &next
		b.LastError = reason
		// Initial round plus more than five failed retries. Persist the marker so
		// daemon restarts and manual retries do not send the same alert repeatedly.
		if b.FailureRounds > 6 && !b.FailureNotified && notify != nil {
			if notifyErr := notify(); notifyErr == nil {
				b.FailureNotified = true
			} else {
				fmt.Fprintln(os.Stderr, "Daylog: 系统通知未提交；请用 daylog status / report 查看持续失败的记录")
			}
		}
		fmt.Fprintf(os.Stderr, "Daylog: 整理失败 · %s · %s · 第 %d 轮 · 下次 %s · 重试：daylog analyze --batch %s --retry\n", b.ID, FailureReason(reason), b.FailureRounds, next.Local().Format("01-02 15:04"), b.ID)
		return WriteJSON(filepath.Join(s.Root, "batches", id+".json"), b)
	})
}

// A user-requested retry only changes eligibility, never counters or evidence.
func (s *Store) RequestRetry(id string) error {
	lock, err := os.OpenFile(filepath.Join(s.Root, "analysis.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = platform.TryLock(lock); err != nil {
		return errors.New("another analysis is running")
	}
	defer platform.Unlock(lock)
	return s.locked(func() error {
		b, err := s.Batch(id)
		if err != nil {
			return err
		}
		if _, err = os.Stat(s.reportPath(id)); err == nil {
			return errors.New("batch already analyzed")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		now := time.Now().UTC()
		b.RetryAfter = &now
		return WriteJSON(filepath.Join(s.Root, "batches", id+".json"), b)
	})
}

// New data gets priority, so a large failed backlog cannot starve current work.
func nextAnalysis(pending []Batch, now time.Time) (Batch, bool) {
	for _, b := range pending {
		if b.Attempts == 0 && b.FailureRounds == 0 && RetryReady(b, now) {
			return b, true
		}
	}
	for _, b := range pending {
		if RetryReady(b, now) {
			return b, true
		}
	}
	return Batch{}, false
}

// A fixed notification contains no screenshot content, credentials or model
// output. macOS notification preferences control whether a banner is shown.
func failureNotification(reason string) string {
	return "活动整理连续重试失败超过 5 次。最近原因：" + FailureReason(reason) + "。截图已保留；运行 daylog status 或 daylog report 查看。"
}

func notifyAnalysisFailure(reason string) error {
	message := failureNotification(reason)
	if runtime.GOOS == "windows" {
		return native.NotifyAnalysisFailure(message)
	}
	if runtime.GOOS != "darwin" {
		return errors.New("system notification unsupported")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/osascript", "-e", fmt.Sprintf(`display notification %q with title "Daylog · 整理需要关注"`, message)).Run()
}

// Safe, stable descriptions for exported reports; raw transport text stays in
// private diagnostics rather than being embedded into the HTML.
func FailureReason(reason string) string {
	switch {
	case strings.Contains(reason, "upstream_timeout"):
		return "模型调用超时（曾发生上游重试）"
	case strings.Contains(reason, "timeout"):
		return "模型等待超时"
	case strings.Contains(reason, "canceled"):
		return "调用被停止或系统暂停"
	case strings.Contains(reason, "evidence"):
		return "截图证据缺失、损坏或校验不符；请检查原文件后重试"
	case strings.Contains(reason, "preparation_storage"):
		return "整理临时文件写入失败，请检查磁盘空间与权限"
	case strings.Contains(reason, "validation") || strings.Contains(reason, "invalid_"):
		return "返回内容缺少有效活动或截图证据"
	case strings.Contains(reason, "cli_exit") || strings.Contains(reason, "agent_error"):
		return "Agent 执行失败，请检查登录或模型状态"
	case strings.Contains(reason, "no_compatible"):
		return "没有可用的本机 Agent"
	case strings.Contains(reason, "http_error"):
		return "模型接口返回错误"
	case strings.Contains(reason, "network"):
		return "模型网络请求失败"
	default:
		return "整理尚未成功，请用 daylog doctor 检查"
	}
}
