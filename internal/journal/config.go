package journal

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	IntervalSeconds     int          `json:"interval_seconds"`
	Height              int          `json:"height"`
	Quality             int          `json:"jpeg_quality"`
	WindowID            uint32       `json:"window_id"`
	IdleSeconds         int          `json:"idle_after_seconds"`
	MinSaveSeconds      int          `json:"min_save_seconds"`
	HeartbeatSeconds    int          `json:"heartbeat_seconds"`
	ChangeThreshold     float64      `json:"change_threshold"`
	BatchSeconds        int          `json:"batch_seconds"`
	MaxImages           int          `json:"max_images_per_batch"`
	RawRetentionDays    int          `json:"raw_retention_days"`
	ReportRetentionDays int          `json:"report_retention_days"`
	MaxStorageBytes     int64        `json:"max_storage_bytes"`
	MaxPendingBatches   int          `json:"max_pending_batches"`
	BlockedApps         []string     `json:"blocked_apps"`
	Power               PowerConfig  `json:"power"`
	Agent               AgentConfig  `json:"agent"`
	Update              UpdateConfig `json:"update"`
	AI                  AIConfig     `json:"ai"`
}

type AgentConfig struct {
	Enabled        bool     `json:"enabled"`
	Order          []string `json:"order"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}
type UpdateConfig struct {
	Source string `json:"source"`
}

type PowerConfig struct {
	BatteryIntervalSeconds  int  `json:"battery_interval_seconds"`
	LowPowerIntervalSeconds int  `json:"low_power_interval_seconds"`
	PauseBelowPercent       int  `json:"pause_below_percent"`
	AnalyzeOnBattery        bool `json:"analyze_on_battery"`
}

type AIConfig struct {
	Enabled           bool   `json:"enabled"`
	Endpoint          string `json:"endpoint"`
	Model             string `json:"model"`
	KeyEnv            string `json:"key_env"`
	MaxRequestsPerDay int    `json:"max_requests_per_day"`
}

func DefaultConfig() Config {
	return Config{IntervalSeconds: 30, Height: 720, Quality: 60, IdleSeconds: 300,
		MinSaveSeconds: 90, HeartbeatSeconds: 300, ChangeThreshold: 0.035,
		BatchSeconds: 900, MaxImages: 6, RawRetentionDays: 3, ReportRetentionDays: 90, MaxStorageBytes: 256 << 20,
		MaxPendingBatches: 96, BlockedApps: []string{},
		Power: PowerConfig{BatteryIntervalSeconds: 120, LowPowerIntervalSeconds: 180, PauseBelowPercent: 20},
		Agent: AgentConfig{Enabled: true, Order: []string{"claude", "codex", "hermes"}, TimeoutSeconds: 90},
		AI:    AIConfig{KeyEnv: "DAYLOG_API_KEY", MaxRequestsPerDay: 48}}
}

func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "Daylog"), nil
}

func (c Config) Validate() error {
	if c.IntervalSeconds < 10 || c.IntervalSeconds > 600 || c.Height < 360 || c.Height > 1080 || c.Quality < 30 || c.Quality > 90 {
		return errors.New("interval must be 10..600 seconds, height 360..1080, quality 30..90")
	}
	if c.IdleSeconds != 0 && c.IdleSeconds < 60 {
		return errors.New("idle threshold must be 0 (disabled) or >=60 seconds")
	}
	if c.MinSaveSeconds < c.IntervalSeconds || c.HeartbeatSeconds < c.MinSaveSeconds || c.ChangeThreshold <= 0 || c.ChangeThreshold > 1 {
		return errors.New("invalid save interval, heartbeat or change threshold")
	}
	if c.BatchSeconds < 300 || c.BatchSeconds > 3600 || c.MaxImages < 1 || c.MaxImages > 12 || c.RawRetentionDays < 1 || c.RawRetentionDays > 30 || c.MaxStorageBytes < 8<<20 || c.MaxPendingBatches < 1 || c.MaxPendingBatches > 256 {
		return errors.New("invalid batch or storage bounds")
	}
	if c.AI.MaxRequestsPerDay < 1 || c.AI.MaxRequestsPerDay > 256 {
		return errors.New("AI daily request budget must be 1..256")
	}
	if c.ReportRetentionDays < 7 || c.ReportRetentionDays > 3650 {
		return errors.New("report retention must be 7..3650 days")
	}
	if c.Agent.TimeoutSeconds < 10 || c.Agent.TimeoutSeconds > 180 || len(c.Agent.Order) > 5 {
		return errors.New("invalid agent timeout or order")
	}
	for _, name := range c.Agent.Order {
		if name != "claude" && name != "codex" && name != "hermes" && name != "grok" && name != "antigravity" {
			return errors.New("unknown agent name")
		}
	}
	if c.Power.BatteryIntervalSeconds < c.IntervalSeconds || c.Power.LowPowerIntervalSeconds < c.Power.BatteryIntervalSeconds || c.Power.LowPowerIntervalSeconds > 1800 || c.Power.PauseBelowPercent < 5 || c.Power.PauseBelowPercent > 50 {
		return errors.New("invalid power policy")
	}
	if c.AI.Enabled {
		if strings.TrimSpace(c.AI.Model) == "" {
			return errors.New("AI model is required")
		}
		if err := validateEndpoint(c.AI.Endpoint); err != nil {
			return err
		}
	}
	if strings.ContainsAny(c.AI.KeyEnv, "=\x00\r\n") {
		return errors.New("invalid key environment variable name")
	}
	return nil
}

func validateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("endpoint must be a full URL without credentials, query or fragment")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("endpoint requires HTTPS, except loopback local models")
}

func ReadConfig(root string) (Config, error) {
	c := DefaultConfig()
	f, err := os.Open(filepath.Join(root, "config.json"))
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, errors.New("invalid config.json; check field names and JSON syntax")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("configuration contains trailing content")
	}
	return c, c.Validate()
}
