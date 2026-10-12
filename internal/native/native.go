package native

import "context"

type Environment struct {
	App            string  `json:"app"`
	IdleSeconds    float64 `json:"idle_seconds"`
	ScreenOff      bool    `json:"screen_off"`
	Locked         bool    `json:"locked"`
	OnBattery      bool    `json:"on_battery"`
	BatteryPercent int     `json:"battery_percent"`
	LowPower       bool    `json:"low_power"`
	Thermal        int     `json:"thermal"`
	Permission     bool    `json:"screen_permission"`
}

type CaptureOptions struct {
	Height   int
	Quality  int
	WindowID uint32
	Blocked  []string
}

type Window struct {
	ID    uint32 `json:"id"`
	App   string `json:"app"`
	Title string `json:"title"`
}

type Source interface {
	Environment(context.Context) (Environment, error)
	Capture(context.Context, CaptureOptions) ([]byte, error)
}

type Mac struct{}
