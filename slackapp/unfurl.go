package slackapp

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// Preview is the metadata shown in a link preview. It never includes file
// content.
type Preview struct {
	Title       string
	ContentType string
	OwnerEmail  string
	OpenThreads int
}

// Previews looks up preview metadata for an artifact slug. Return an error for
// unknown or internal slugs; the link is then left without a preview.
type Previews interface {
	Preview(ctx context.Context, slug string) (Preview, error)
}

// Unfurler answers link_shared events for Trove artifact links.
type Unfurler struct {
	api      API
	previews Previews
	host     string
}

// NewUnfurler previews links whose host matches baseURL's host.
func NewUnfurler(api API, previews Previews, baseURL string) (*Unfurler, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("slack unfurls need an absolute base URL, got %q", baseURL)
	}
	return &Unfurler{api: api, previews: previews, host: strings.ToLower(u.Host)}, nil
}

// HandleLinkShared unfurls the Trove links in one shared message.
func (u *Unfurler) HandleLinkShared(ctx context.Context, ev *slackevents.LinkSharedEvent) {
	// Links still in the message composer carry a UUID instead of a message
	// timestamp and need an unfurl ID this event type doesn't expose; skip them.
	if _, err := strconv.ParseFloat(ev.MessageTimeStamp, 64); err != nil {
		log.Printf("INFO  slack unfurl skipped reason=composer channel=%s", ev.Channel)
		return
	}
	if ok, reason := u.channelAllowsPreviews(ctx, ev.Channel); !ok {
		log.Printf("INFO  slack unfurl skipped reason=%s channel=%s", reason, ev.Channel)
		return
	}

	unfurls := make(map[string]slack.Attachment, len(ev.Links))
	for _, link := range ev.Links {
		slug, ok := u.artifactSlug(link.URL)
		if !ok {
			continue
		}
		preview, err := u.previews.Preview(ctx, slug)
		if err != nil {
			log.Printf("INFO  slack unfurl skipped reason=no_preview slug=%s: %v", slug, err)
			continue
		}
		unfurls[link.URL] = attachment(link.URL, preview)
	}
	if len(unfurls) == 0 {
		return
	}
	if _, _, _, err := u.api.UnfurlMessageContext(ctx, ev.Channel, ev.MessageTimeStamp, unfurls); err != nil {
		log.Printf("WARN  slack unfurl failed channel=%s links=%d: %v", ev.Channel, len(unfurls), err)
		return
	}
	log.Printf("INFO  slack unfurl sent channel=%s links=%d", ev.Channel, len(unfurls))
}

// channelAllowsPreviews refuses channels shared with other organizations, and
// fails closed when the channel can't be checked.
func (u *Unfurler) channelAllowsPreviews(ctx context.Context, channelID string) (bool, string) {
	ch, err := u.api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: channelID})
	if err != nil {
		return false, "channel_lookup_failed"
	}
	if ch.IsExtShared || ch.IsPendingExtShared {
		return false, "externally_shared_channel"
	}
	return true, ""
}

// artifactSlug returns the slug for /{slug}, /{slug}/raw and site page URLs on
// this deployment's host.
func (u *Unfurler) artifactSlug(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Host, u.host) {
		return "", false
	}
	slug, _, _ := strings.Cut(strings.TrimPrefix(parsed.Path, "/"), "/")
	if slug == "" || strings.HasPrefix(slug, "_") {
		return "", false
	}
	return slug, true
}

func attachment(link string, p Preview) slack.Attachment {
	title := p.Title
	if title == "" {
		title = "Trove artifact"
	}
	var details []string
	if p.ContentType != "" {
		details = append(details, escape(strings.TrimSpace(strings.Split(p.ContentType, ";")[0])))
	}
	if p.OwnerEmail != "" {
		details = append(details, "uploaded by "+escape(p.OwnerEmail))
	}
	switch p.OpenThreads {
	case 0:
	case 1:
		details = append(details, "1 open comment thread")
	default:
		details = append(details, fmt.Sprintf("%d open comment threads", p.OpenThreads))
	}
	blocks := []slack.Block{
		slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*<%s|%s>*", link, linkText(title)), false, false), nil, nil),
	}
	if len(details) > 0 {
		blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(slack.MarkdownType, strings.Join(details, " · "), false, false)))
	}
	return slack.Attachment{Blocks: slack.Blocks{BlockSet: blocks}}
}
