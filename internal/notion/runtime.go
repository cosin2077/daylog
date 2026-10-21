package notion

import (
	"context"
	"errors"
	"os"
	"time"

	"daylog/internal/exports"
	"daylog/internal/journal"
)

func (s *Service) Engine() exports.Engine {
	return exports.Engine{Root: s.Settings.StateRoot(s.Root), Sink: s, Load: func(day time.Time) (exports.Day, error) {
		store, err := journal.Open(s.Root)
		if err != nil {
			return exports.Day{}, err
		}
		return exports.LoadDay(store, day)
	}}
}

// Network-only work continues while the screen is locked; it never requests
// capture permission or starts analysis. Resume/start immediately checks dates
// missed while the computer was off. Missing config means no network traffic.
func Run(ctx context.Context, root string, secrets SecretStore) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		settings, err := ReadSettings(root)
		if err == nil && settings.Enabled && settings.Layout == DailyLayout {
			zone, zoneErr := time.LoadLocation(settings.Timezone)
			since, dateErr := time.Time{}, zoneErr
			if zoneErr == nil {
				since, dateErr = time.ParseInLocation("2006-01-02", settings.Since, zone)
			}
			if zoneErr == nil && dateErr == nil {
				token := secrets.Get(settings.SecretName())
				client := NewClient(token)
				service := Service{Root: root, Settings: settings, Client: client}
				config, configErr := journal.ReadConfig(root)
				if configErr == nil {
					jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
					_ = service.Engine().RunDue(jobCtx, since, time.Now().In(zone), config.ReportRetentionDays)
					cancel()
				}
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) { /* status/check exposes malformed settings without printing data */
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
