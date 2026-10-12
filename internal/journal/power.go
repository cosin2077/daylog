package journal

import (
	"daylog/internal/native"
	"time"
)

type Policy struct {
	Delay       time.Duration
	PauseReason string
	CanAnalyze  bool
}

func PowerPolicy(c Config, env native.Environment) Policy {
	p := Policy{Delay: time.Duration(c.IntervalSeconds) * time.Second, CanAnalyze: !env.OnBattery || c.Power.AnalyzeOnBattery}
	if env.OnBattery {
		p.Delay = time.Duration(c.Power.BatteryIntervalSeconds) * time.Second
	}
	if env.LowPower || env.Thermal == 1 {
		p.Delay = time.Duration(c.Power.LowPowerIntervalSeconds) * time.Second
	}
	switch {
	case env.Locked:
		p.PauseReason = "screen_locked"
	case env.ScreenOff:
		p.PauseReason = "screen_asleep"
	case env.Thermal >= 2:
		p.PauseReason = "thermal_pressure"
	case env.OnBattery && env.BatteryPercent >= 0 && env.BatteryPercent <= c.Power.PauseBelowPercent:
		p.PauseReason = "low_battery"
	}
	if p.PauseReason != "" {
		p.CanAnalyze = false
		if p.Delay < time.Minute {
			p.Delay = time.Minute
		}
	}
	return p
}
