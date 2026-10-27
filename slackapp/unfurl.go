package slackapp

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
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
	Card          *preview.Card   // author-made preview image, if any

	cardFileID string // Slack file ID for Card, set by the unfurler
}

// Reasons content is withheld from a preview.
const (
	HiddenFlagged  = "flagged during content review"
	hiddenByChoice = "shared as title only"
)

// CommentExcerpt is a short quote from a comment.
type CommentExcerpt struct {
	AuthorEmail string
	Body        string
	URL         string // opens the thread in Trove's comments sidebar
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
	cards    *cardUploader
	// retryDelay is how long to wait before retrying an unfurl whose image
	// Slack may still be processing.
	retryDelay time.Duration
	// respond posts to an interaction's response_url, e.g. to remove a prompt.
	respond func(ctx context.Context, responseURL string, payload any) error
}

// NewUnfurler previews links whose host matches baseURL's host.
func NewUnfurler(api API, previews Previews, baseURL string) (*Unfurler, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("slack unfurls need an absolute base URL, got %q", baseURL)
	}
	return &Unfurler{api: api, previews: previews, host: strings.ToLower(u.Host), guests: newGuestDirectory(api), cards: newCardUploader(api), retryDelay: 2 * time.Second, respond: postResponseURL}, nil
}

// HandleLinkShared unfurls the Trove links in one shared message. Previews
// post directly where everyone is a workspace member; where guests are present
// or the bot can't see who is (DMs it isn't in), the sharer is asked first.
func (u *Unfurler) HandleLinkShared(ctx context.Context, ev *slackevents.LinkSharedEvent) {
	// Links still in the message composer carry a UUID instead of a message
	// timestamp and need an unfurl ID this event type doesn't expose; skip them.
	if _, err := strconv.ParseFloat(ev.MessageTimeStamp, 64); err != nil {
		log.Printf("INFO  slack unfurl skipped reason=composer channel=%s", ev.Channel)
		return
	}
	links := make([]string, 0, len(ev.Links))
	for _, link := range ev.Links {
		if _, ok := u.artifactSlug(link.URL); ok && !slices.Contains(links, link.URL) {
			links = append(links, link.URL)
		}
	}
	if len(links) == 0 {
		return
	}
	switch aud := u.audience(ctx, ev.Channel); aud {
	case audienceExternal:
		log.Printf("INFO  slack unfurl skipped reason=externally_shared_channel channel=%s", ev.Channel)
	case audienceMembers:
		u.sendPreviews(ctx, ev.Channel, ev.MessageTimeStamp, links, choiceFull)
	default:
		u.askSharer(ctx, ev, links, aud)
	}
}

// sendPreviews unfurls links in a posted message. choiceTitle withholds
// content as for flagged artifacts.
func (u *Unfurler) sendPreviews(ctx context.Context, channel, ts string, links []string, choice consentChoice) {
	previews := make(map[string]Preview, len(links))
	for _, link := range links {
		slug, ok := u.artifactSlug(link)
		if !ok {
			continue
		}
		p, err := u.previews.Preview(ctx, slug)
		if err != nil {
			log.Printf("INFO  slack unfurl skipped reason=no_preview slug=%s: %v", slug, err)
			continue
		}
		previews[link] = p
	}
	if len(previews) == 0 {
		return
	}
	withCards := 0
	for link, p := range previews {
		if choice == choiceTitle && p.HiddenReason == "" {
			p.HiddenReason = hiddenByChoice
		}
		if p.Card != nil && p.HiddenReason == "" {
			id, err := u.cards.fileID(ctx, p.Card)
			if err != nil {
				log.Printf("WARN  slack unfurl card unavailable link=%s; using text preview: %v", link, err)
			} else {
				p.cardFileID = id
				withCards++
			}
		}
		previews[link] = p
	}

	err := u.unfurl(ctx, channel, ts, previews)
	if err != nil && withCards > 0 {
		// A just-uploaded image can take a moment to process; retry once, then
		// fall back to text previews.
		if sleepErr := sleepContext(ctx, u.retryDelay); sleepErr == nil {
			err = u.unfurl(ctx, channel, ts, previews)
		}
		if err != nil {
			log.Printf("WARN  slack unfurl with card failed channel=%s; retrying as text: %v", channel, err)
			for link, p := range previews {
				if p.cardFileID != "" {
					u.cards.reject(p.Card)
					p.cardFileID = ""
					previews[link] = p
				}
			}
			withCards = 0
			err = u.unfurl(ctx, channel, ts, previews)
		}
	}
	if err != nil {
		log.Printf("WARN  slack unfurl failed channel=%s links=%d: %v", channel, len(previews), err)
		return
	}
	log.Printf("INFO  slack unfurl sent channel=%s links=%d cards=%d content=%s", channel, len(previews), withCards, choice)
}

func (u *Unfurler) unfurl(ctx context.Context, channel, ts string, previews map[string]Preview) error {
	unfurls := make(map[string]slack.Attachment, len(previews))
	for link, p := range previews {
		unfurls[link] = attachment(link, p)
	}
	_, _, _, err := u.api.UnfurlMessageContext(ctx, channel, ts, unfurls)
	return err
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// audience classifies who can read a conversation. Unknown covers DMs the bot
// isn't in, where Slack won't say who the other people are, and failed checks.
func (u *Unfurler) audience(ctx context.Context, channelID string) audience {
	ch, err := u.api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: channelID})
	if err != nil {
		log.Printf("INFO  slack unfurl channel lookup failed channel=%s: %v", channelID, err)
		return audienceUnknown
	}
	if ch.IsExtShared || ch.IsPendingExtShared {
		return audienceExternal
	}
	hasGuests, err := u.guests.hasGuests(ctx, channelID)
	if err != nil {
		log.Printf("WARN  slack unfurl guest check failed channel=%s: %v", channelID, err)
		return audienceUnknown
	}
	if hasGuests {
		return audienceGuests
	}
	return audienceMembers
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
	hasCard := !hidden && p.cardFileID != ""
	if hasCard {
		alt := p.Card.Alt
		if alt == "" {
			alt = "Preview of " + title
		}
		blocks = append(blocks, slack.NewImageBlockSlackFile(&slack.SlackFileObject{ID: p.cardFileID}, alt, "", nil))
	}

	if !hidden {
		if len(p.Stats) > 0 {
			fields := make([]*slack.TextBlockObject, 0, len(p.Stats))
			for _, st := range p.Stats {
				fields = append(fields, slack.NewTextBlockObject(slack.MarkdownType, "*"+escape(st.Label)+"*\n"+escape(st.Value), false, false))
			}
			blocks = append(blocks, slack.NewSectionBlock(nil, fields, nil))
		}
		if p.Snippet != "" && !hasCard {
			blocks = append(blocks, markdownSection("```"+escape(strings.ReplaceAll(p.Snippet, "```", "`\u200b``"))+"```"))
		}
		if len(p.Sections) > 0 && !hasCard {
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
		text := "“" + escape(excerpt(p.LatestComment.Body, commentExcerptRunes)) + "”"
		if p.LatestComment.URL != "" {
			text = "<" + p.LatestComment.URL + "|" + strings.ReplaceAll(text, "|", "¦") + ">"
		}
		quote := ":speech_balloon: " + text + " — " + escape(p.LatestComment.AuthorEmail)
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
