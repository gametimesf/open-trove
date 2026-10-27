package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/internal/config"
	"github.com/gametimesf/open-trove/notify"
	"github.com/gametimesf/open-trove/preview"
	"github.com/gametimesf/open-trove/slackapp"
	"github.com/gametimesf/open-trove/storage"
)

var errNotArtifact = errors.New("not an artifact")

// artifactInfo describes stored artifacts for notifications and link previews.
type artifactInfo struct {
	store   storage.Store
	baseURL string
}

// artifactDescription is what notifications and previews know about an artifact.
type artifactDescription struct {
	summary preview.Summary
	owner   string
	flagged bool
}

// describe summarizes an artifact's content. Flagged artifacts are not read,
// and a failed read degrades to a metadata-only summary.
func (a artifactInfo) describe(ctx context.Context, slug string) (artifactDescription, error) {
	if isInternalSlug(slug) || validateSlug(slug) != nil {
		return artifactDescription{}, errNotArtifact
	}
	isSite, err := a.store.HeadSite(ctx, slug)
	if err != nil {
		return artifactDescription{}, fmt.Errorf("checking site: %w", err)
	}
	if isSite {
		manifest, err := a.store.GetSiteManifest(ctx, slug)
		if err != nil {
			return artifactDescription{}, fmt.Errorf("reading site manifest: %w", err)
		}
		entry, err := a.readSiteEntry(ctx, slug, manifest.Entry)
		if err != nil {
			log.Printf("WARN  preview content unavailable slug=%s: %v", slug, err)
		}
		summary := preview.SummarizeSite(entry, manifest.FileCount)
		summary.Title = cmp.Or(summary.Title, slug)
		return artifactDescription{summary: summary, owner: manifest.OwnerEmail}, nil
	}
	meta, err := a.store.Metadata(ctx, slug)
	if err != nil {
		return artifactDescription{}, fmt.Errorf("reading metadata: %w", err)
	}
	var head []byte
	if !meta.Flagged {
		if head, err = a.readHead(ctx, slug); err != nil {
			log.Printf("WARN  preview content unavailable slug=%s: %v", slug, err)
		}
	}
	summary := preview.Summarize(meta.ContentType, meta.Filename, head)
	summary.Title = cmp.Or(summary.Title, meta.Filename, slug)
	return artifactDescription{summary: summary, owner: meta.OwnerEmail, flagged: meta.Flagged}, nil
}

// readHead reads the leading bytes preview.Summarize needs.
func (a artifactInfo) readHead(ctx context.Context, slug string) ([]byte, error) {
	body, _, err := a.store.Get(ctx, slug, fmt.Sprintf("bytes=0-%d", preview.MaxBytes))
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(io.LimitReader(body, preview.MaxBytes+1))
}

func (a artifactInfo) readSiteEntry(ctx context.Context, slug, entry string) ([]byte, error) {
	body, _, err := a.store.GetSiteFile(ctx, slug, entry)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(io.LimitReader(body, preview.MaxBytes))
}

// Artifact implements notify.ArtifactLookup.
func (a artifactInfo) Artifact(ctx context.Context, slug string) (notify.Artifact, error) {
	d, err := a.describe(ctx, slug)
	if err != nil {
		return notify.Artifact{}, err
	}
	return notify.Artifact{Title: d.summary.Title, OwnerEmail: d.owner}, nil
}

// Preview implements slackapp.Previews.
func (a artifactInfo) Preview(ctx context.Context, slug string) (slackapp.Preview, error) {
	d, err := a.describe(ctx, slug)
	if err != nil {
		return slackapp.Preview{}, err
	}
	stored, err := a.store.ListComments(ctx, slug)
	if err != nil {
		return slackapp.Preview{}, fmt.Errorf("listing comments: %w", err)
	}
	p := slackapp.Preview{Summary: d.summary, OwnerEmail: d.owner}
	if d.flagged {
		p.HiddenReason = slackapp.HiddenFlagged
	} else {
		card, err := a.store.GetCard(ctx, slug)
		switch {
		case err == nil:
			p.Card = &card
		case !errors.Is(err, preview.ErrCardNotFound):
			log.Printf("WARN  preview card unavailable slug=%s: %v", slug, err)
		}
	}
	var latest *comments.Comment
	for i, c := range stored {
		if c.ParentID != "" || c.ResolvedAt != nil || c.DeletedAt != nil {
			continue
		}
		p.OpenThreads++
		if latest == nil || c.CreatedAt.After(latest.CreatedAt) {
			latest = &stored[i]
		}
	}
	if latest != nil {
		p.LatestComment = &slackapp.CommentExcerpt{
			AuthorEmail: latest.AuthorEmail,
			Body:        latest.Body,
			URL:         notify.CommentURL(a.baseURL, slug, latest.Anchor.Resource, latest.ThreadID),
		}
	}
	return p, nil
}

// startSlack connects the Slack app when enabled and returns the comment
// service options it needs. Background work stops when ctx is done.
func startSlack(ctx context.Context, cfg config.Slack, baseURL string, store storage.Store) ([]comments.Option, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	api := slack.New(cfg.BotToken, slack.OptionAppLevelToken(cfg.AppToken))
	info := artifactInfo{store: store, baseURL: baseURL}
	var opts []comments.Option

	if cfg.Notifications {
		dispatcher := notify.NewDispatcher(slackapp.NewNotifier(api), info, store, baseURL)
		go dispatcher.Run(ctx)
		opts = append(opts, comments.WithObserver(dispatcher))
	}
	if cfg.Unfurls {
		unfurler, err := slackapp.NewUnfurler(api, info, baseURL)
		if err != nil {
			return nil, err
		}
		client := socketmode.New(api)
		go func() {
			if err := client.RunContext(ctx); err != nil && ctx.Err() == nil {
				log.Printf("ERROR slack socket mode stopped: %v", err)
			}
		}()
		go slackapp.Serve(ctx, client.Events, client, unfurler)
	}
	log.Printf("slack enabled notifications=%t unfurls=%t", cfg.Notifications, cfg.Unfurls)
	return opts, nil
}
