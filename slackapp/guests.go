package slackapp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

const (
	guestListTTL    = time.Hour
	channelGuestTTL = 10 * time.Minute
	memberPageSize  = 1000
	maxMemberPages  = 10
)

// guestDirectory reports whether a conversation includes guest accounts
// (single- or multi-channel guests). Guests are outside the organization, so
// previews in their channels show metadata only. Lookups fail closed: an error
// or an oversized channel counts as having guests.
type guestDirectory struct {
	api API
	now func() time.Time

	mu       sync.Mutex
	guests   map[string]bool
	guestsAt time.Time
	channels map[string]channelGuests
}

type channelGuests struct {
	has bool
	at  time.Time
}

func newGuestDirectory(api API) *guestDirectory {
	return &guestDirectory{api: api, now: time.Now, channels: make(map[string]channelGuests)}
}

// hasGuests reports whether the channel may include guests, and why it was
// unable to tell when it fails closed.
func (g *guestDirectory) hasGuests(ctx context.Context, channelID string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if c, ok := g.channels[channelID]; ok && now.Sub(c.at) < channelGuestTTL {
		return c.has, nil
	}
	if err := g.refreshGuests(ctx, now); err != nil {
		return true, err
	}
	has, err := g.channelHasGuest(ctx, channelID)
	if err != nil {
		return true, err
	}
	g.channels[channelID] = channelGuests{has: has, at: now}
	return has, nil
}

func (g *guestDirectory) refreshGuests(ctx context.Context, now time.Time) error {
	if g.guests != nil && now.Sub(g.guestsAt) < guestListTTL {
		return nil
	}
	users, err := g.api.GetUsersContext(ctx)
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}
	guests := make(map[string]bool)
	for _, u := range users {
		if !u.Deleted && (u.IsRestricted || u.IsUltraRestricted) {
			guests[u.ID] = true
		}
	}
	g.guests, g.guestsAt = guests, now
	// Membership answers depend on the guest list; recheck channels.
	clear(g.channels)
	return nil
}

func (g *guestDirectory) channelHasGuest(ctx context.Context, channelID string) (bool, error) {
	params := &slack.GetUsersInConversationParameters{ChannelID: channelID, Limit: memberPageSize}
	for range maxMemberPages {
		members, cursor, err := g.api.GetUsersInConversationContext(ctx, params)
		if err != nil {
			return true, fmt.Errorf("listing channel members: %w", err)
		}
		for _, id := range members {
			if g.guests[id] {
				return true, nil
			}
		}
		if cursor == "" {
			return false, nil
		}
		params.Cursor = cursor
	}
	return true, fmt.Errorf("channel has more than %d members", memberPageSize*maxMemberPages)
}
