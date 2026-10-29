package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/gametimesf/open-trove/internal/config"
	"github.com/gametimesf/open-trove/preview"
)

// troveClientHeader identifies the uploading tool and version, e.g.
// "trove-skill/1.3.0". It is optional and only used for hints and logs.
const troveClientHeader = "X-Trove-Client"

// HintClientUpdate is the hint code for an outdated upload client.
const HintClientUpdate = "client_update"

// uploadClient returns the client name and version from X-Trove-Client, if any.
func uploadClient(c echo.Context) (name, version string) {
	name, version, _ = strings.Cut(strings.TrimSpace(c.Request().Header.Get(troveClientHeader)), "/")
	return strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(version)
}

// clientHint suggests an update when a known client sends an older version.
func (s *server) clientHint(c echo.Context) []preview.Hint {
	name, version := uploadClient(c)
	latest, ok := s.latestClients[name]
	if !ok || !config.ValidVersion(version) || !config.OlderVersion(version, latest.Version) {
		return nil
	}
	msg := fmt.Sprintf("A newer %s (%s) is available; this upload used %s.", name, latest.Version, version)
	if latest.Update != "" {
		msg += " " + latest.Update
	}
	return []preview.Hint{{Code: HintClientUpdate, Message: msg}}
}

// hasCard reports whether the artifact already has a preview card.
func (s *server) hasCard(ctx context.Context, slug string) bool {
	_, err := s.store.GetCard(ctx, slug)
	return err == nil
}

// withSlug makes hint messages concrete for this artifact.
func withSlug(hints []preview.Hint, slug string) []preview.Hint {
	for i := range hints {
		hints[i].Message = strings.ReplaceAll(hints[i].Message, "{slug}", url.PathEscape(slug))
	}
	return hints
}

// head returns at most preview.MaxBytes of data for preview checks.
func head(data []byte) []byte {
	return data[:min(len(data), preview.MaxBytes)]
}
