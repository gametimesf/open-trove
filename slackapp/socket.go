package slackapp

import (
	"context"
	"log"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// acker acknowledges Socket Mode requests; *socketmode.Client implements it.
type acker interface {
	Ack(req socketmode.Request, payload ...any) error
}

// Serve handles Socket Mode events until ctx is done or events closes. Run the
// client's RunContext separately; it owns the connection and reconnects.
func Serve(ctx context.Context, events <-chan socketmode.Event, ack acker, unfurler *Unfurler) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			handleEvent(ctx, evt, ack, unfurler)
		}
	}
}

func handleEvent(ctx context.Context, evt socketmode.Event, ack acker, unfurler *Unfurler) {
	switch evt.Type {
	case socketmode.EventTypeConnected:
		log.Printf("INFO  slack socket mode connected")
	case socketmode.EventTypeConnectionError, socketmode.EventTypeInvalidAuth:
		log.Printf("WARN  slack socket mode %s", evt.Type)
	case socketmode.EventTypeEventsAPI:
		// Ack first: Slack retries unacknowledged events after a few seconds.
		if evt.Request != nil {
			if err := ack.Ack(*evt.Request); err != nil {
				log.Printf("WARN  slack ack failed: %v", err)
			}
		}
		api, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		if shared, ok := api.InnerEvent.Data.(*slackevents.LinkSharedEvent); ok && unfurler != nil {
			unfurler.HandleLinkShared(ctx, shared)
		}
	case socketmode.EventTypeInteractive:
		// Button clicks on preview prompts. Ack first, as for events.
		if evt.Request != nil {
			if err := ack.Ack(*evt.Request); err != nil {
				log.Printf("WARN  slack ack failed: %v", err)
			}
		}
		cb, ok := evt.Data.(slack.InteractionCallback)
		if ok && cb.Type == slack.InteractionTypeBlockActions && unfurler != nil {
			unfurler.HandleConsent(ctx, cb)
		}
	}
}
