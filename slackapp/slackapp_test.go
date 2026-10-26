package slackapp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/notify"
	"github.com/gametimesf/open-trove/preview"
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
	allUsers    []slack.User
	usersErr    error
	userLists   int
	members     [][]string // pages of conversation members
	membersErr  error
	memberCalls int
	uploads     []slack.UploadFileParameters
	uploadErr   error
	unfurlErrs  []error // returned by successive UnfurlMessageContext calls
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

func (f *fakeAPI) GetUsersContext(context.Context, ...slack.GetUsersOption) ([]slack.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userLists++
	return f.allUsers, f.usersErr
}

func (f *fakeAPI) GetUsersInConversationContext(_ context.Context, params *slack.GetUsersInConversationParameters) ([]string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.memberCalls++
	if f.membersErr != nil {
		return nil, "", f.membersErr
	}
	page := 0
	if params.Cursor != "" {
		page, _ = strconv.Atoi(params.Cursor)
	}
	if page >= len(f.members) {
		return nil, "", nil
	}
	next := ""
	if page+1 < len(f.members) {
		next = strconv.Itoa(page + 1)
	}
	return f.members[page], next, nil
}

func (f *fakeAPI) UnfurlMessageContext(_ context.Context, channelID, ts string, unfurls map[string]slack.Attachment, _ ...slack.MsgOption) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unfurlCalls = append(f.unfurlCalls, unfurlCall{channel: channelID, ts: ts, unfurls: unfurls})
	if len(f.unfurlErrs) > 0 {
		err := f.unfurlErrs[0]
		f.unfurlErrs = f.unfurlErrs[1:]
		return "", "", "", err
	}
	return channelID, ts, "", nil
}

func (f *fakeAPI) UploadFileContext(_ context.Context, params slack.UploadFileParameters) (*slack.FileSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, params)
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	return &slack.FileSummary{ID: fmt.Sprintf("F%d", len(f.uploads))}, nil
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
	previews := fakePreviews{"q3-plan": {Summary: preview.Summary{Kind: preview.KindHTML, Title: "Q3 plan"}, OwnerEmail: "owner@example.com", OpenThreads: 2}}
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

// blockTexts flattens an attachment's blocks into their visible mrkdwn text.
func blockTexts(a slack.Attachment) []string {
	var out []string
	for _, b := range a.Blocks.BlockSet {
		switch b := b.(type) {
		case *slack.SectionBlock:
			if b.Text != nil {
				out = append(out, b.Text.Text)
			}
			for _, f := range b.Fields {
				out = append(out, "field:"+f.Text)
			}
		case *slack.ContextBlock:
			out = append(out, "context:"+b.ContextElements.Elements[0].(*slack.TextBlockObject).Text)
		}
	}
	return out
}

func TestPreviewAttachmentContent(t *testing.T) {
	const link = "https://trove.example.com/q3-plan"
	rich := Preview{
		Summary: preview.Summary{
			Kind: preview.KindMarkdown, Title: "Q3 <plan> | v2", Description: "Why we <changed> course & what's next.",
			Sections: []string{"Summary", "Risks & mitigations"},
			Stats:    []preview.Stat{{Label: "Revenue", Value: "$1.2M"}, {Label: "Orders", Value: "<4k>"}},
			Facts:    []string{"4 min read"},
			Snippet:  "a ``` b <c>",
		},
		OwnerEmail: "owner@example.com", OpenThreads: 2,
		LatestComment: &CommentExcerpt{AuthorEmail: "pat@example.com", Body: "Can we   split <this> section?"},
	}
	tests := []struct {
		name string
		p    Preview
		want []string
	}{
		{
			name: "rich summary",
			p:    rich,
			want: []string{
				"*<" + link + "|Q3 &lt;plan&gt; ¦ v2>*\nWhy we &lt;changed&gt; course &amp; what's next.",
				"field:*Revenue*\n$1.2M", "field:*Orders*\n&lt;4k&gt;",
				"```a `\u200b`` b &lt;c&gt;```",
				"context:*Contents:* Summary · Risks &amp; mitigations",
				"context:Markdown · 4 min read · uploaded by owner@example.com · 2 open comment threads",
				"context::speech_balloon: “Can we split &lt;this&gt; section?” — pat@example.com",
			},
		},
		{
			name: "hidden reason withholds content but keeps metadata",
			p: func() Preview {
				p := rich
				p.HiddenReason = HiddenFlagged
				return p
			}(),
			want: []string{
				"*<" + link + "|Q3 &lt;plan&gt; ¦ v2>*",
				"context:Markdown · uploaded by owner@example.com · 2 open comment threads",
				"context::lock: Preview content hidden: flagged during content review",
			},
		},
		{
			name: "minimal",
			p:    Preview{},
			want: []string{"*<" + link + "|Trove artifact>*", "context:File"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := attachment(link, tt.p)
			if a.Color != accentColor {
				t.Errorf("color = %q", a.Color)
			}
			if got := blockTexts(a); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

type fakeAcker struct{ acked []string }

func (f *fakeAcker) Ack(req socketmode.Request, _ ...any) error {
	f.acked = append(f.acked, req.EnvelopeID)
	return nil
}

func TestServeAcksAndRoutesLinkShared(t *testing.T) {
	api := &fakeAPI{channel: &slack.Channel{}}
	u, _ := NewUnfurler(api, fakePreviews{"q3-plan": {Summary: preview.Summary{Title: "Q3"}}}, "https://trove.example.com")
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

func TestUnfurlerHidesContentWhenGuestsPresent(t *testing.T) {
	const link = "https://trove.example.com/q3-plan"
	previews := fakePreviews{"q3-plan": {Summary: preview.Summary{Kind: preview.KindHTML, Title: "Q3", Description: "Internal numbers"}}}
	users := []slack.User{{ID: "U1"}, {ID: "G1", IsRestricted: true}, {ID: "G2", IsUltraRestricted: true}, {ID: "G3", IsRestricted: true, Deleted: true}}
	tests := []struct {
		name       string
		members    [][]string
		usersErr   error
		membersErr error
		wantHidden bool
	}{
		{name: "members only", members: [][]string{{"U1", "G3"}}},
		{name: "multi-channel guest", members: [][]string{{"U1", "G1"}}, wantHidden: true},
		{name: "single-channel guest on a later page", members: [][]string{{"U1"}, {"G2"}}, wantHidden: true},
		{name: "user list unavailable", members: [][]string{{"U1"}}, usersErr: errors.New("ratelimited"), wantHidden: true},
		{name: "members unavailable", membersErr: errors.New("missing_scope"), wantHidden: true},
		{name: "too many members to check", members: make([][]string, maxMemberPages+1), wantHidden: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeAPI{channel: &slack.Channel{}, allUsers: users, usersErr: tt.usersErr, members: tt.members, membersErr: tt.membersErr}
			u, err := NewUnfurler(api, previews, "https://trove.example.com")
			if err != nil {
				t.Fatal(err)
			}
			u.HandleLinkShared(context.Background(), &slackevents.LinkSharedEvent{Channel: "C1", MessageTimeStamp: "1717000000.000100", Links: []slackevents.SharedLinks{{URL: link}}})
			if len(api.unfurlCalls) != 1 {
				t.Fatalf("unfurl calls = %d, want 1", len(api.unfurlCalls))
			}
			texts := strings.Join(blockTexts(api.unfurlCalls[0].unfurls[link]), "\n")
			hidden := strings.Contains(texts, "Preview content hidden: this conversation includes guests")
			if hidden != tt.wantHidden || strings.Contains(texts, "Internal numbers") == tt.wantHidden {
				t.Fatalf("hidden = %t, want %t:\n%s", hidden, tt.wantHidden, texts)
			}
		})
	}
}

func TestGuestDirectoryCaches(t *testing.T) {
	api := &fakeAPI{allUsers: []slack.User{{ID: "G1", IsRestricted: true}}, members: [][]string{{"U1"}}}
	g := newGuestDirectory(api)
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	ctx := context.Background()

	for range 2 {
		if has, err := g.hasGuests(ctx, "C1"); has || err != nil {
			t.Fatalf("hasGuests = %t, %v", has, err)
		}
	}
	if api.userLists != 1 || api.memberCalls != 1 {
		t.Fatalf("user lists = %d member calls = %d, want 1 and 1", api.userLists, api.memberCalls)
	}

	now = now.Add(channelGuestTTL)
	api.members = [][]string{{"U1", "G1"}}
	if has, _ := g.hasGuests(ctx, "C1"); !has {
		t.Fatal("expected guests after the channel cache expired")
	}
	if api.userLists != 1 || api.memberCalls != 2 {
		t.Fatalf("user lists = %d member calls = %d, want 1 and 2", api.userLists, api.memberCalls)
	}
}

// imageFileIDs returns the Slack file IDs referenced by image blocks.
func imageFileIDs(a slack.Attachment) []string {
	var ids []string
	for _, b := range a.Blocks.BlockSet {
		if img, ok := b.(*slack.ImageBlock); ok && img.SlackFile != nil {
			ids = append(ids, img.SlackFile.ID)
		}
	}
	return ids
}

func TestUnfurlerCards(t *testing.T) {
	const link = "https://trove.example.com/q3-plan"
	card := &preview.Card{Image: []byte("png"), ContentType: "image/png", SHA256: strings.Repeat("a", 64), Alt: "Q3 at a glance"}
	withCard := Preview{
		Summary: preview.Summary{Kind: preview.KindHTML, Title: "Q3", Description: "Summary", Snippet: "excerpt", Sections: []string{"One"}},
		Card:    card,
	}
	event := &slackevents.LinkSharedEvent{Channel: "C1", MessageTimeStamp: "1717000000.000100", Links: []slackevents.SharedLinks{{URL: link}}}
	tests := []struct {
		name        string
		preview     Preview
		members     [][]string
		uploadErr   error
		unfurlErrs  []error
		wantUploads int
		wantCalls   int
		wantImage   bool
	}{
		{name: "card replaces excerpt", preview: withCard, wantUploads: 1, wantCalls: 1, wantImage: true},
		{name: "upload failure falls back to text", preview: withCard, uploadErr: errors.New("missing_scope"), wantUploads: 1, wantCalls: 1},
		{name: "slack still processing: retry with card", preview: withCard, unfurlErrs: []error{errors.New("invalid_blocks")}, wantUploads: 1, wantCalls: 2, wantImage: true},
		{name: "slack rejects card: fall back to text", preview: withCard, unfurlErrs: []error{errors.New("invalid_blocks"), errors.New("invalid_blocks")}, wantUploads: 1, wantCalls: 3},
		{name: "guests: no upload", preview: withCard, members: [][]string{{"G1"}}, wantCalls: 1},
		{name: "flagged: no upload", preview: func() Preview { p := withCard; p.HiddenReason = HiddenFlagged; return p }(), wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeAPI{
				channel: &slack.Channel{}, allUsers: []slack.User{{ID: "G1", IsRestricted: true}}, members: tt.members,
				uploadErr: tt.uploadErr, unfurlErrs: tt.unfurlErrs,
			}
			u, err := NewUnfurler(api, fakePreviews{"q3-plan": tt.preview}, "https://trove.example.com")
			if err != nil {
				t.Fatal(err)
			}
			u.retryDelay = 0
			u.HandleLinkShared(context.Background(), event)

			if len(api.uploads) != tt.wantUploads || len(api.unfurlCalls) != tt.wantCalls {
				t.Fatalf("uploads = %d, unfurl calls = %d; want %d and %d", len(api.uploads), len(api.unfurlCalls), tt.wantUploads, tt.wantCalls)
			}
			if tt.wantUploads > 0 && (api.uploads[0].AltTxt != "Q3 at a glance" || api.uploads[0].Channel != "" || len(api.uploads[0].Channels) != 0) {
				t.Errorf("upload params = %+v; must not share to a conversation", api.uploads[0])
			}
			last := api.unfurlCalls[len(api.unfurlCalls)-1].unfurls[link]
			ids := imageFileIDs(last)
			texts := strings.Join(blockTexts(last), "\n")
			if tt.wantImage {
				if len(ids) != 1 || ids[0] != "F1" || strings.Contains(texts, "excerpt") || strings.Contains(texts, "Contents") {
					t.Fatalf("want card instead of excerpt; ids=%v\n%s", ids, texts)
				}
				return
			}
			if len(ids) != 0 {
				t.Fatalf("unexpected image %v", ids)
			}
		})
	}
}

func TestCardUploaderCachesAndBacksOff(t *testing.T) {
	api := &fakeAPI{}
	u := newCardUploader(api)
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	u.now = func() time.Time { return now }
	ctx := context.Background()
	card := &preview.Card{Image: []byte("png"), ContentType: "image/png", SHA256: strings.Repeat("b", 64)}

	for range 2 {
		if id, err := u.fileID(ctx, card); err != nil || id != "F1" {
			t.Fatalf("fileID = %q, %v", id, err)
		}
	}
	if len(api.uploads) != 1 || api.uploads[0].Filename != "trove-preview-bbbbbbbbbbbb.png" {
		t.Fatalf("uploads = %+v", api.uploads)
	}

	u.reject(card)
	if _, err := u.fileID(ctx, card); err == nil || len(api.uploads) != 1 {
		t.Fatalf("expected back-off after rejection; err=%v uploads=%d", err, len(api.uploads))
	}
	now = now.Add(cardFailureTTL)
	if id, err := u.fileID(ctx, card); err != nil || id != "F2" {
		t.Fatalf("fileID after back-off = %q, %v", id, err)
	}
}
