package slackapp

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/gametimesf/open-trove/preview"
)

// Preview is what a link preview shows: a content summary plus artifact
// metadata. When HiddenReason is set, only the title and metadata are shown.
type Preview struct {
	preview.Summary
	OwnerEmail    string
	OpenThreads   int
	LatestComment *CommentExcerpt // newest open thread root, if any
	HiddenReason  string          // why content excerpts are withheld, e.g. flagged during review
}

// Reasons content is withheld from a preview.
const (
	HiddenFlagged = "flagged during content review"
	hiddenGuests  = "this conversation includes guests"
)

// CommentExcerpt is a short quote from a comment.
type CommentExcerpt struct {
	AuthorEmail string
	Body        string
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
	guests   *guestDirectory
}

// NewUnfurler previews links whose host matches baseURL's host.
func NewUnfurler(api API, previews Previews, baseURL string) (*Unfurler, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("slack unfurls need an absolute base URL, got %q", baseURL)
	}
	return &Unfurler{api: api, previews: previews, host: strings.ToLower(u.Host), guests: newGuestDirectory(api)}, nil
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
	previews := make(map[string]Preview, len(ev.Links))
	for _, link := range ev.Links {
		slug, ok := u.artifactSlug(link.URL)
		if !ok {
			continue
		}
		p, err := u.previews.Preview(ctx, slug)
		if err != nil {
			log.Printf("INFO  slack unfurl skipped reason=no_preview slug=%s: %v", slug, err)
			continue
		}
		previews[link.URL] = p
	}
	if len(previews) == 0 {
		return
	}
	hasGuests, err := u.guests.hasGuests(ctx, ev.Channel)
	if err != nil {
		log.Printf("WARN  slack unfurl guest check failed channel=%s; hiding content: %v", ev.Channel, err)
	}
	unfurls := make(map[string]slack.Attachment, len(previews))
	for link, p := range previews {
		if hasGuests && p.HiddenReason == "" {
			p.HiddenReason = hiddenGuests
		}
		unfurls[link] = attachment(link, p)
	}
	if _, _, _, err := u.api.UnfurlMessageContext(ctx, ev.Channel, ev.MessageTimeStamp, unfurls); err != nil {
		log.Printf("WARN  slack unfurl failed channel=%s links=%d: %v", ev.Channel, len(unfurls), err)
		return
	}
	log.Printf("INFO  slack unfurl sent channel=%s links=%d content_hidden=%t", ev.Channel, len(unfurls), hasGuests)
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

// accentColor is the bar beside every Trove preview.
const accentColor = "#0F766E"

// commentExcerptRunes bounds the quoted latest comment.
const commentExcerptRunes = 140

func attachment(link string, p Preview) slack.Attachment {
	title := p.Title
	if title == "" {
		title = "Trove artifact"
	}
	head := fmt.Sprintf("*<%s|%s>*", link, linkText(title))
	hidden := p.HiddenReason != ""
	if p.Description != "" && !hidden {
		head += "\n" + escape(p.Description)
	}
	blocks := []slack.Block{markdownSection(head)}

	if !hidden {
		if len(p.Stats) > 0 {
			fields := make([]*slack.TextBlockObject, 0, len(p.Stats))
			for _, st := range p.Stats {
				fields = append(fields, slack.NewTextBlockObject(slack.MarkdownType, "*"+escape(st.Label)+"*\n"+escape(st.Value), false, false))
			}
			blocks = append(blocks, slack.NewSectionBlock(nil, fields, nil))
		}
		if p.Snippet != "" {
			blocks = append(blocks, markdownSection("```"+escape(strings.ReplaceAll(p.Snippet, "```", "`\u200b``"))+"```"))
		}
		if len(p.Sections) > 0 {
			escaped := make([]string, 0, len(p.Sections))
			for _, sec := range p.Sections {
				escaped = append(escaped, escape(sec))
			}
			blocks = append(blocks, contextLine("*Contents:* "+strings.Join(escaped, " · ")))
		}
	}

	details := []string{escape(p.Kind.Label())}
	if !hidden {
		for _, f := range p.Facts {
			details = append(details, escape(f))
		}
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
	blocks = append(blocks, contextLine(strings.Join(details, " · ")))

	switch {
	case hidden:
		blocks = append(blocks, contextLine(":lock: Preview content hidden: "+escape(p.HiddenReason)))
	case p.LatestComment != nil && p.LatestComment.Body != "":
		quote := fmt.Sprintf(":speech_balloon: “%s” — %s", escape(excerpt(p.LatestComment.Body, commentExcerptRunes)), escape(p.LatestComment.AuthorEmail))
		blocks = append(blocks, contextLine(quote))
	}
	return slack.Attachment{Color: accentColor, Blocks: slack.Blocks{BlockSet: blocks}}
}

func markdownSection(text string) *slack.SectionBlock {
	return slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, text, false, false), nil, nil)
}

func contextLine(text string) *slack.ContextBlock {
	return slack.NewContextBlock("", slack.NewTextBlockObject(slack.MarkdownType, text, false, false))
}

// excerpt collapses whitespace and shortens text to n runes.
func excerpt(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	return strings.TrimSpace(string([]rune(text)[:n-1])) + "…"
}
