package download

import (
	"context"
	"fmt"

	"github.com/Bink-lab/Shino/internal/lua"
	"github.com/Bink-lab/Shino/internal/steam"
)

func DownloadAll(ctx context.Context, cfg *lua.Config, manifestsDir, outputDir string, maxPar int, onProgress func(depotID uint32, done, total uint64), onLog func(string)) ([]string, error) {
	logf := func(format string, args ...any) {
		if onLog != nil {
			onLog(fmt.Sprintf(format, args...))
		}
	}
	var warnings []string
	succeeded := 0
	var lastErr error
	logf("connecting to steam")
	sess, err := steam.ConnectAnonymous(ctx)
	if err != nil {
		logf("connection failed: %v", err)
		return warnings, err
	}
	defer sess.Close()
	logf("steam session ready")
	servers, err := sess.Servers(ctx)
	if err != nil {
		logf("cdn servers lookup failed: %v", err)
		return warnings, err
	}
	var hosts []string
	for _, s := range servers {
		if s.Host == "" || s.SteamChinaOnly {
			continue
		}
		hosts = append(hosts, s.Host)
		if len(hosts) >= 6 {
			break
		}
	}
	if len(hosts) == 0 {
		logf("no cdn hosts available")
		return warnings, fmt.Errorf("no cdn hosts")
	}
	logf("using %d cdn hosts", len(hosts))
	for i, dep := range cfg.Depots {
		if err := ctx.Err(); err != nil {
			return warnings, err
		}
		logf("depot %d: starting (%d/%d)", dep.ID, i+1, len(cfg.Depots))
		w, err := DownloadDepot(ctx, sess, hosts, Options{
			AppID:        cfg.AppID,
			Depot:        dep,
			ManifestsDir: manifestsDir,
			OutputDir:    outputDir,
			MaxParallel:  maxPar,
			OnProgress: func(done, total uint64) {
				if onProgress != nil {
					onProgress(dep.ID, done, total)
				}
			},
			OnLog: onLog,
		})
		warnings = append(warnings, w...)
		for _, line := range w {
			logf("warn: %s", line)
		}
		if err != nil {
			skipped := fmt.Sprintf("depot %d skipped: %v", dep.ID, err)
			logf("warn: %s", skipped)
			warnings = append(warnings, skipped)
			lastErr = err
			continue
		}
		succeeded++
	}
	if lastErr != nil {
		return warnings, fmt.Errorf("%d of %d depots failed: %w", len(cfg.Depots)-succeeded, len(cfg.Depots), lastErr)
	}
	logf("all depots complete")
	return warnings, nil
}
