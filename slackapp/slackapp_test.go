package slackapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/notify"
)

// fakeAPI records calls and returns configured results.
type fakeAPI struct {
	mu          sync.Mutex
	users       map[string]*slack.User
	userErr     error
	lookups     int
	posts       []postCall
	postErr     error
	channel     *slack.Channel
	channelErr  error
	unfurlCalls []unfurlCall
}

type postCall struct {
	channel string
	values  string
}

type unfurlCall struct {
	channel, ts string
	unfurls     map[string]slack.Attachment
}

func (f *fakeAPI) GetUserByEmailContext(_ context.Context, email string) (*slack.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.userErr != nil {
		return nil, f.userErr
	}
	if u, ok := f.users[email]; ok {
		return u, nil
	}
	return nil, slack.SlackErrorResponse{Err: "users_not_found"}
}

func (f *fakeAPI) PostMessageContext(_ context.Context, channelID string, options ...slack.MsgOption) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.postErr != nil {
		return "", "", f.postErr
	}
	_, values, err := slack.UnsafeApplyMsgOptions("token", channelID, "https://slack.com/api/", options...)
	if err != nil {
		return "", "", err
	}
	f.posts = append(f.posts, postCall{channel: channelID, values: values.Encode()})
	return channelID, "1.1", nil
}

func (f *fakeAPI) GetConversationInfoContext(context.Context, *slack.GetConversationInfoInput) (*slack.Channel, error) {
	return f.channel, f.channelErr
}

func (f *fakeAPI) UnfurlMessageContext(_ context.Context, channelID, ts string, unfurls map[string]slack.Attachment, _ ...slack.MsgOption) (string, string, string, error) {
	f.unfurlCalls = append(f.unfurlCalls, unfurlCall{channel: channelID, ts: ts, unfurls: unfurls})
	return channelID, ts, "", nil
}

var reply = notify.Notification{
	Kind: comments.ActivityReplyCreated, RecipientEmail: "owner@example.com", ActorEmail: "a<b>@example.com",
	ArtifactTitle: "Q3 | plan", ArtifactURL: "https://trove.example.com/q3-plan", Excerpt: "ship <it> & go",
}

func TestNotifierSendsDirectMessage(t *testing.T) {
	api := &fakeAPI{users: map[string]*slack.User{"owner@example.com": {ID: "U123"}}}
	n := NewNotifier(api)
	if err := n.Notify(context.Background(), reply); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if len(api.posts) != 1 || api.posts[0].channel != "U123" {
		t.Fatalf("posts = %+v, want one DM to U123", api.posts)
	}
	for _, want := range []string{
		"text=a%26lt%3Bb%26gt%3B%40example.com+replied", // fallback text escaped
		"unfurl_links=false",
	} {
		if !strings.Contains(api.posts[0].values, want) {
			t.Errorf("message missing %q in %s", want, api.posts[0].values)
		}
	}

	blocks := messageBlocks(reply)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	summary := blocks[0].(*slack.SectionBlock).Text.Text
	if summary != "*a&lt;b&gt;@example.com* replied to a thread on <https://trove.example.com/q3-plan|Q3 ¦ plan>" {
		t.Errorf("summary = %q", summary)
	}
	if quote := blocks[1].(*slack.SectionBlock).Text.Text; quote != "> ship &lt;it&gt; &amp; go" {
		t.Errorf("quote = %q", quote)
	}
	resolved := reply
	resolved.Kind, resolved.Excerpt = comments.ActivityThreadResolved, ""
	if got := messageBlocks(resolved); len(got) != 1 {
		t.Errorf("resolution blocks = %d, want 1 (no excerpt)", len(got))
	}
}

func TestNotifierUserLookup(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		api           *fakeAPI
		wantUndeliver bool
		wantErr       bool
	}{
		{name: "no slack account is undeliverable", api: &fakeAPI{}, wantUndeliver: true, wantErr: true},
		{name: "deactivated user is undeliverable", api: &fakeAPI{users: map[string]*slack.User{"owner@example.com": {ID: "U1", Deleted: true}}}, wantUndeliver: true, wantErr: true},
		{name: "rate limit is transient", api: &fakeAPI{userErr: &slack.RateLimitedError{RetryAfter: time.Second}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := NewNotifier(tt.api)
			n.now = func() time.Time { return now }
			err := n.Notify(context.Background(), reply)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Notify error = %v", err)
			}
			if errors.Is(err, notify.ErrUndeliverable) != tt.wantUndeliver {
				t.Fatalf("undeliverable = %v, want %v (%v)", errors.Is(err, notify.ErrUndeliverable), tt.wantUndeliver, err)
			}
		})
	}
}

func TestNotifierCachesLookups(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	api := &fakeAPI{users: map[string]*slack.User{"owner@example.com": {ID: "U123"}}}
	n := NewNotifier(api)
	n.now = func() time.Time { return now }
	for range 3 {
		if err := n.Notify(context.Background(), reply); err != nil {
			t.Fatal(err)
		}
	}
	if api.lookups != 1 {
		t.Fatalf("lookups = %d, want 1 (cached)", api.lookups)
	}
	now = now.Add(userCacheTTL + time.Minute)
	if err := n.Notify(context.Background(), reply); err != nil {
		t.Fatal(err)
	}
	if api.lookups != 2 {
		t.Fatalf("lookups after TTL = %d, want 2", api.lookups)
	}

	missing := &fakeAPI{}
	m := NewNotifier(missing)
	m.now = func() time.Time { return now }
	_ = m.Notify(context.Background(), reply)
	_ = m.Notify(context.Background(), reply)
	if missing.lookups != 1 {
		t.Fatalf("missing-user lookups = %d, want 1 (negative cache)", missing.lookups)
	}
}

func TestNotifierClassifiesPostErrors(t *testing.T) {
	api := &fakeAPI{users: map[string]*slack.User{"owner@example.com": {ID: "U123"}}, postErr: slack.SlackErrorResponse{Err: "cannot_dm_bot"}}
	if err := NewNotifier(api).Notify(context.Background(), reply); !errors.Is(err, notify.ErrUndeliverable) {
		t.Fatalf("error = %v, want undeliverable", err)
	}
	api.postErr = slack.SlackErrorResponse{Err: "internal_error"}
	if err := NewNotifier(api).Notify(context.Background(), reply); err == nil || errors.Is(err, notify.ErrUndeliverable) {
		t.Fatalf("error = %v, want transient", err)
	}
}

type fakePreviews map[string]Preview

func (f fakePreviews) Preview(_ context.Context, slug string) (Preview, error) {
	p, ok := f[slug]
	if !ok {
		return Preview{}, errors.New("not found")
	}
	return p, nil
}

func TestUnfurler(t *testing.T) {
	previews := fakePreviews{"q3-plan": {Title: "Q3 plan.html", ContentType: "text/html; charset=utf-8", OwnerEmail: "owner@example.com", OpenThreads: 2}}
	links := func(urls ...string) []slackevents.SharedLinks {
		out := make([]slackevents.SharedLinks, 0, len(urls))
		for _, u := range urls {
			out = append(out, slackevents.SharedLinks{URL: u})
		}
		return out
	}
	tests := []struct {
		name       string
		channel    *slack.Channel
		channelErr error
		ts         string
		links      []slackevents.SharedLinks
		wantLinks  []string
	}{
		{
			name: "previews artifact, raw and site page links on our host", channel: &slack.Channel{}, ts: "1717000000.000100",
			links:     links("https://trove.example.com/q3-plan", "https://TROVE.example.com/q3-plan/raw", "https://trove.example.com/q3-plan/pages/a.html"),
			wantLinks: []string{"https://trove.example.com/q3-plan", "https://TROVE.example.com/q3-plan/raw", "https://trove.example.com/q3-plan/pages/a.html"},
		},
		{
			name: "ignores other hosts, internal and unknown slugs", channel: &slack.Channel{}, ts: "1717000000.000100",
			links: links("https://evil.example.com/q3-plan", "https://trove.example.com/_sites/x", "https://trove.example.com/missing", "https://trove.example.com/"),
		},
		{
			name: "skips externally shared channels", ts: "1717000000.000100",
			channel: &slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{IsExtShared: true}}},
			links:   links("https://trove.example.com/q3-plan"),
		},
		{
			name: "skips pending external shares", ts: "1717000000.000100",
			channel: &slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{IsPendingExtShared: true}}},
			links:   links("https://trove.example.com/q3-plan"),
		},
		{
			name: "fails closed when the channel can't be checked", channelErr: errors.New("missing_scope"), ts: "1717000000.000100",
			links: links("https://trove.example.com/q3-plan"),
		},
		{
			name: "skips composer previews", channel: &slack.Channel{}, ts: "6a1b2c3d-0000-4000-8000-000000000000",
			links: links("https://trove.example.com/q3-plan"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeAPI{channel: tt.channel, channelErr: tt.channelErr}
			u, err := NewUnfurler(api, previews, "https://trove.example.com")
			if err != nil {
				t.Fatal(err)
			}
			u.HandleLinkShared(context.Background(), &slackevents.LinkSharedEvent{Channel: "C1", MessageTimeStamp: tt.ts, Links: tt.links})
			if len(tt.wantLinks) == 0 {
				if len(api.unfurlCalls) != 0 {
					t.Fatalf("unexpected unfurl: %+v", api.unfurlCalls)
				}
				return
			}
			if len(api.unfurlCalls) != 1 {
				t.Fatalf("unfurl calls = %d, want 1", len(api.unfurlCalls))
			}
			call := api.unfurlCalls[0]
			if call.channel != "C1" || call.ts != tt.ts || len(call.unfurls) != len(tt.wantLinks) {
				t.Fatalf("unfurl call = %+v", call)
			}
			for _, link := range tt.wantLinks {
				if _, ok := call.unfurls[link]; !ok {
					t.Errorf("missing unfurl for %s", link)
				}
			}
		})
	}
}

func TestPreviewAttachmentContent(t *testing.T) {
	a := attachment("https://trove.example.com/q3-plan", Preview{Title: "Q3 <plan>", ContentType: "text/html; charset=utf-8", OwnerEmail: "owner@example.com", OpenThreads: 1})
	if len(a.Blocks.BlockSet) != 2 {
		t.Fatalf("blocks = %d, want 2", len(a.Blocks.BlockSet))
	}
	title := a.Blocks.BlockSet[0].(*slack.SectionBlock).Text.Text
	if title != "*<https://trove.example.com/q3-plan|Q3 &lt;plan&gt;>*" {
		t.Errorf("title = %q", title)
	}
	ctx := a.Blocks.BlockSet[1].(*slack.ContextBlock).ContextElements.Elements[0].(*slack.TextBlockObject).Text
	if ctx != "text/html · uploaded by owner@example.com · 1 open comment thread" {
		t.Errorf("context = %q", ctx)
	}
}

type fakeAcker struct{ acked []string }

func (f *fakeAcker) Ack(req socketmode.Request, _ ...any) error {
	f.acked = append(f.acked, req.EnvelopeID)
	return nil
}

func TestServeAcksAndRoutesLinkShared(t *testing.T) {
	api := &fakeAPI{channel: &slack.Channel{}}
	u, _ := NewUnfurler(api, fakePreviews{"q3-plan": {Title: "Q3"}}, "https://trove.example.com")
	events := make(chan socketmode.Event, 2)
	events <- socketmode.Event{
		Type:    socketmode.EventTypeEventsAPI,
		Request: &socketmode.Request{EnvelopeID: "env-1"},
		Data: slackevents.EventsAPIEvent{InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.LinkSharedEvent{
			Channel: "C1", MessageTimeStamp: "1717000000.000100", Links: []slackevents.SharedLinks{{URL: "https://trove.example.com/q3-plan"}},
		}}},
	}
	close(events)
	ack := &fakeAcker{}
	Serve(context.Background(), events, ack, u)
	if len(ack.acked) != 1 || ack.acked[0] != "env-1" {
		t.Fatalf("acked = %v", ack.acked)
	}
	if len(api.unfurlCalls) != 1 {
		t.Fatalf("unfurl calls = %d, want 1", len(api.unfurlCalls))
	}
}
