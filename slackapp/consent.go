package slackapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// audience is who can read a conversation, as far as the bot can tell.
type audience int

const (
	audienceMembers  audience = iota // workspace members only: preview directly
	audienceGuests                   // includes single- or multi-channel guests: ask the sharer
	audienceUnknown                  // the bot can't see the members (DMs it isn't in): ask the sharer
	audienceExternal                 // shared with another organization: never preview
)

// consentChoice is the sharer's answer to a preview prompt.
type consentChoice string

const (
	choiceFull  consentChoice = "full"
	choiceTitle consentChoice = "title"
	choiceNone  consentChoice = "none"
)

// Prompt button action IDs carry the choice after this prefix.
const consentActionPrefix = "trove_unfurl_"

// maxButtonValue is Slack's limit on a button's value.
const maxButtonValue = 2000

const responseTimeout = 5 * time.Second

// consentRequest is what a prompt button carries back: the message to unfurl
// and who may answer for it.
type consentRequest struct {
	Channel string   `json:"c"`
	TS      string   `json:"t"`
	User    string   `json:"u"`
	Links   []string `json:"l"`
}

// askSharer shows the person who posted the links a private prompt asking
// whether to preview them here.
func (u *Unfurler) askSharer(ctx context.Context, ev *slackevents.LinkSharedEvent, links []string, aud audience) {
	req := consentRequest{Channel: ev.Channel, TS: ev.MessageTimeStamp, User: ev.User}
	for _, link := range links {
		req.Links = append(req.Links, link)
		if b, _ := json.Marshal(req); len(b) > maxButtonValue {
			req.Links = req.Links[:len(req.Links)-1]
			break
		}
	}
	if len(req.Links) == 0 {
		return
	}
	value, err := json.Marshal(req)
	if err != nil {
		log.Printf("ERROR slack unfurl prompt encode channel=%s: %v", ev.Channel, err)
		return
	}
	question := "Trove can't see who's in this conversation. Show a preview of this Trove link? Only if everyone here works at Gametime."
	reason := "unknown_audience"
	if aud == audienceGuests {
		question = ":warning: This conversation includes guests (people outside Gametime). Show a preview of this Trove link with its content?"
		reason = "guests"
	}
	button := func(choice consentChoice, label string, style slack.Style) *slack.ButtonBlockElement {
		b := slack.NewButtonBlockElement(consentActionPrefix+string(choice), string(value), slack.NewTextBlockObject(slack.PlainTextType, label, false, false))
		b.Style = style
		return b
	}
	blocks := []slack.Block{
		markdownSection(question),
		slack.NewActionBlock("trove_unfurl_prompt",
			button(choiceFull, "Show preview", slack.StylePrimary),
			button(choiceTitle, "Title only", ""),
			button(choiceNone, "Don't preview", ""),
		),
	}
	_, _, _, err = u.api.UnfurlMessageContext(ctx, ev.Channel, ev.MessageTimeStamp, map[string]slack.Attachment{},
		slack.MsgOptionUnfurlAuthRequired(ev.MessageTimeStamp),
		slack.MsgOptionUnfurlAuthBlocks(ev.MessageTimeStamp, blocks...),
	)
	if err != nil {
		log.Printf("WARN  slack unfurl prompt failed channel=%s: %v", ev.Channel, err)
		return
	}
	log.Printf("INFO  slack unfurl prompt sent reason=%s channel=%s links=%d", reason, ev.Channel, len(req.Links))
}

// HandleConsent applies the sharer's answer to a preview prompt and removes
// the prompt. Answers from anyone but the sharer are ignored.
func (u *Unfurler) HandleConsent(ctx context.Context, cb slack.InteractionCallback) {
	for _, action := range cb.ActionCallback.BlockActions {
		choice, ok := strings.CutPrefix(action.ActionID, consentActionPrefix)
		if !ok {
			continue
		}
		var req consentRequest
		if err := json.Unmarshal([]byte(action.Value), &req); err != nil {
			log.Printf("WARN  slack unfurl consent ignored reason=bad_value: %v", err)
			continue
		}
		if req.User == "" || req.User != cb.User.ID {
			log.Printf("WARN  slack unfurl consent ignored reason=not_sharer user=%s channel=%s", cb.User.ID, req.Channel)
			continue
		}
		if _, err := strconv.ParseFloat(req.TS, 64); err != nil || req.Channel == "" {
			log.Printf("WARN  slack unfurl consent ignored reason=bad_message user=%s", cb.User.ID)
			continue
		}
		links := make([]string, 0, len(req.Links))
		for _, link := range req.Links {
			if _, ok := u.artifactSlug(link); ok {
				links = append(links, link)
			}
		}
		c := consentChoice(choice)
		log.Printf("INFO  slack unfurl consent choice=%s user=%s channel=%s links=%d", c, cb.User.ID, req.Channel, len(links))
		switch c {
		case choiceFull, choiceTitle:
			u.sendPreviews(ctx, req.Channel, req.TS, links, c)
		case choiceNone:
		default:
			log.Printf("WARN  slack unfurl consent ignored reason=unknown_choice choice=%q", choice)
		}
		if cb.ResponseURL != "" {
			if err := u.respond(ctx, cb.ResponseURL, map[string]any{"delete_original": true}); err != nil {
				log.Printf("WARN  slack unfurl prompt cleanup failed: %v", err)
			}
		}
		return
	}
}

// postResponseURL posts a JSON payload to a Slack interaction response URL.
func postResponseURL(ctx context.Context, responseURL string, payload any) error {
	u, err := url.Parse(responseURL)
	if err != nil || u.Scheme != "https" || (u.Hostname() != "slack.com" && !strings.HasSuffix(u.Hostname(), ".slack.com")) {
		return fmt.Errorf("refusing response URL outside slack.com")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, responseTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, responseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("response URL returned %s", resp.Status)
	}
	return nil
}
