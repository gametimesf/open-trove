package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/internal/config"
	"github.com/gametimesf/open-trove/notify"
	"github.com/gametimesf/open-trove/slackapp"
	"github.com/gametimesf/open-trove/storage"
)

var errNotArtifact = errors.New("not an artifact")

// artifactInfo describes stored artifacts for notifications and link previews.
type artifactInfo struct {
	store storage.Store
}

func (a artifactInfo) describe(ctx context.Context, slug string) (title, contentType, owner string, err error) {
	if isInternalSlug(slug) || validateSlug(slug) != nil {
		return "", "", "", errNotArtifact
	}
	isSite, err := a.store.HeadSite(ctx, slug)
	if err != nil {
		return "", "", "", fmt.Errorf("checking site: %w", err)
	}
	if isSite {
		manifest, err := a.store.GetSiteManifest(ctx, slug)
		if err != nil {
			return "", "", "", fmt.Errorf("reading site manifest: %w", err)
		}
		return slug, "website", manifest.OwnerEmail, nil
	}
	meta, err := a.store.Metadata(ctx, slug)
	if err != nil {
		return "", "", "", fmt.Errorf("reading metadata: %w", err)
	}
	title = meta.Filename
	if title == "" {
		title = slug
	}
	return title, meta.ContentType, meta.OwnerEmail, nil
}

// Artifact implements notify.ArtifactLookup.
func (a artifactInfo) Artifact(ctx context.Context, slug string) (notify.Artifact, error) {
	title, _, owner, err := a.describe(ctx, slug)
	if err != nil {
		return notify.Artifact{}, err
	}
	return notify.Artifact{Title: title, OwnerEmail: owner}, nil
}

// Preview implements slackapp.Previews.
func (a artifactInfo) Preview(ctx context.Context, slug string) (slackapp.Preview, error) {
	title, contentType, owner, err := a.describe(ctx, slug)
	if err != nil {
		return slackapp.Preview{}, err
	}
	stored, err := a.store.ListComments(ctx, slug)
	if err != nil {
		return slackapp.Preview{}, fmt.Errorf("listing comments: %w", err)
	}
	open := 0
	for _, c := range stored {
		if c.ParentID == "" && c.ResolvedAt == nil && c.DeletedAt == nil {
			open++
		}
	}
	return slackapp.Preview{Title: title, ContentType: contentType, OwnerEmail: owner, OpenThreads: open}, nil
}

// startSlack connects the Slack app when enabled and returns the comment
// service options it needs. Background work stops when ctx is done.
func startSlack(ctx context.Context, cfg config.Slack, baseURL string, store storage.Store) ([]comments.Option, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	api := slack.New(cfg.BotToken, slack.OptionAppLevelToken(cfg.AppToken))
	info := artifactInfo{store: store}
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
