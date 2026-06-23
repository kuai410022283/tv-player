package services

import (
	"log/slog"
	"strconv"
	"time"
)

// StartSyncCron initiates the background goroutine that polls the master node for updates.
// It checks settings periodically to see if it should run.
func (s *SyncService) StartSyncCron(channelSvc *ChannelService) {
	slog.Info("Background sync cron started")
	go func() {
		for {
			enable, _ := channelSvc.GetSetting("sync_enable")
			if enable != "true" {
				// If disabled, sleep briefly and check again
				time.Sleep(1 * time.Minute)
				continue
			}

			intervalStr, _ := channelSvc.GetSetting("sync_interval_min")
			interval, err := strconv.Atoi(intervalStr)
			if err != nil || interval < 1 {
				interval = 5 // default to 5 minutes
			}

			// Perform sync
			masterURL, _ := channelSvc.GetSetting("sync_master_url")
			masterToken, _ := channelSvc.GetSetting("sync_master_token")

			if masterURL != "" {
				slog.Info("Starting scheduled auto-sync from master", "masterURL", masterURL)
				err := s.SyncFromMaster(masterURL, masterToken)
				if err != nil {
					slog.Error("Scheduled auto-sync failed", "error", err)
				} else {
					slog.Info("Scheduled auto-sync completed successfully")
				}
			}

			// Sleep for the defined interval
			time.Sleep(time.Duration(interval) * time.Minute)
		}
	}()
}
