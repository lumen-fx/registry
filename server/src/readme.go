package src

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// How long an answer from GitHub is reused before asking again. A README
// changes rarely, a missing one appears when a publisher adds it, and a failed
// fetch is worth retrying soon but not on every page view.
const (
	readmeFreshFor       = 24 * time.Hour
	readmeMissingRetryIn = 6 * time.Hour
	readmeFailureRetryIn = 15 * time.Minute
)

func readmeExpired(entry readmeCache, now time.Time) bool {
	age := now.Sub(entry.CheckedAt)
	switch entry.Status {
	case statusMissing:
		return age >= readmeMissingRetryIn
	case statusUnavailable:
		return age >= readmeFailureRetryIn
	default:
		return age >= readmeFreshFor
	}
}

// readmeFor returns the README of one release, reading GitHub only when the
// cached copy has expired. force skips the expiry check, which is what the
// collector does so a page view rarely pays for a fetch.
//
// A release with no README yields nil, not an error: a package that documents
// nothing is ordinary.
func (s *Server) readmeFor(ctx context.Context, release Release, force bool) (*Readme, error) {
	entry, err := s.getReadme(ctx, release.ID)
	switch {
	case errors.Is(err, ErrReadmeNotCached):
		entry = &readmeCache{ReleaseID: release.ID}
	case err != nil:
		return nil, err
	case !force && !readmeExpired(*entry, time.Now()):
		return readmeOf(release, *entry), nil
	}

	s.refreshReadme(ctx, release, entry)

	// A cache that cannot be written still serves the fetch it just made; the
	// cost is the next reader fetching again.
	if err := s.saveReadme(ctx, *entry); err != nil {
		LoggerFrom(ctx).Error("cache readme",
			slog.String("release", release.ID.String()), slog.Any("error", err))
	}

	return readmeOf(release, *entry), nil
}

// refreshReadme asks GitHub and writes the outcome into entry. A failure keeps
// the markdown already there, so an outage shows the last good README rather
// than an empty panel.
func (s *Server) refreshReadme(ctx context.Context, release Release, entry *readmeCache) {
	ref, ok := releaseRefFor(release)
	if !ok {
		entry.Status = statusMissing
		entry.Markdown = ""
		entry.SourceURL = ""
		entry.FetchedAt = nil
		return
	}

	markdown, source, err := s.source.readme(ctx, ref)
	switch {
	case errors.Is(err, ErrNoReadme):
		// The repository is gone, private, or documents nothing. Whatever was
		// cached no longer reflects it.
		entry.Status = statusMissing
		entry.Markdown = ""
		entry.SourceURL = ""
		entry.FetchedAt = nil
	case err != nil:
		LoggerFrom(ctx).Warn("fetch readme",
			slog.String("repository", ref.String()), slog.Any("error", err))
		entry.Status = statusUnavailable
	default:
		now := time.Now()
		entry.Status = statusOK
		entry.Markdown = markdown
		entry.SourceURL = source
		entry.FetchedAt = &now
	}
}

func readmeOf(release Release, entry readmeCache) *Readme {
	if entry.Markdown == "" {
		return nil
	}
	return &Readme{
		Version:   release.Version,
		Markdown:  entry.Markdown,
		Source:    entry.SourceURL,
		FetchedAt: entry.FetchedAt,
		Stale:     entry.Status != statusOK,
	}
}
