package app

import (
	"context"
	"time"

	"supercli/internal/system/config"
	"supercli/internal/system/uilang"
	"supercli/internal/system/updater"
	"supercli/internal/ui/tui"
)

// startUpdateNotice shows one transcript line when a newer release exists.
// It runs after startup in the background, uses the daily cached result,
// never downloads and stays silent when offline or when update_notify is off.
func startUpdateNotice(send func(any), dataDir, home string) {
	global, _ := config.FindTomlPaths(dataDir, home)
	tc, err := config.LoadToml(global)
	if err != nil || !config.UpdateNotifyEnabled(tc) {
		return
	}
	go func() {
		defer recoverAndLog(dataDir)()
		time.Sleep(3 * time.Second) // keep the first frame and resume IO first
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		state, err := updater.CachedCheck(ctx, dataDir, nil)
		if err != nil || !state.Available || state.LatestVersion == "" {
			return
		}
		send(tui.SystemNoticeMsgFrom(uilang.Format(updateLanguage(dataDir, home), "update.notify_available", state.LatestVersion)))
	}()
}
