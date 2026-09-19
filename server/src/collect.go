package src

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Collect samples GitHub once for the whole registry: the download counter of
// every release asset, and the README of the newest release of each package.
// It is the only writer of either, so the read path serves what the last run
// left behind and a page view rarely waits on GitHub.
//
// GitHub keeps no history of its download counters, so the daily series is
// built from these samples: a day the collector does not run leaves a gap, and
// the next run attributes the whole gap to the day it ran.
func Collect(ctx context.Context, logger *slog.Logger, db *pgxpool.Pool) error {
	server := NewServer(db)

	targets, err := server.collectTargets(ctx)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var sampled, snapshots, readmes, skipped, failed int
	for _, target := range targets {
		log := logger.With(
			slog.String("package", target.PackageName),
			slog.String("version", target.Release.Version))

		ref, ok := releaseRefFor(target.Release)
		if !ok {
			// Nothing is wrong with a release hosted elsewhere; there is just
			// nothing to read.
			skipped++
			continue
		}

		counts, err := server.source.assetDownloads(ctx, ref)
		switch {
		case errors.Is(err, ErrGitHubLimited):
			// Every remaining call would fail the same way, and the samples
			// already written are still a good day's data.
			return err
		case errors.Is(err, ErrNoGitHubRef):
			skipped++
			continue
		case err != nil:
			log.Warn("read download counts", slog.Any("error", err))
			failed++
			continue
		}
		sampled++

		for _, artifact := range target.Release.Artifacts {
			_, asset, ok := parseReleaseURL(artifact.URL)
			if !ok {
				continue
			}
			count, ok := counts[asset]
			if !ok {
				// The release no longer carries the file the registry points
				// at. The old samples stay; a new one would be a guess.
				continue
			}
			if err := server.saveDownloadSnapshot(ctx, artifact.ID, day, count); err != nil {
				log.Warn("save snapshot", slog.String("target", artifact.Target), slog.Any("error", err))
				failed++
				continue
			}
			snapshots++
		}

		// Only the newest release is shown on a package page.
		if target.Newest {
			if _, err := server.readmeFor(ctx, target.Release, true); err != nil {
				log.Warn("refresh readme", slog.Any("error", err))
				failed++
				continue
			}
			readmes++
		}
	}

	logger.Info("collected from github",
		slog.String("day", day.Format(time.DateOnly)),
		slog.Int("releases", sampled),
		slog.Int("snapshots", snapshots),
		slog.Int("readmes", readmes),
		slog.Int("skipped", skipped),
		slog.Int("failed", failed))

	return nil
}
