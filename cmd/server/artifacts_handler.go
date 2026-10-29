package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/gametimesf/open-trove/storage"
)

// ArtifactResponse describes a stored artifact without its content.
type ArtifactResponse struct {
	Slug string `json:"slug" example:"q3-review"`
	// Kind is "file" or "site".
	Kind string `json:"kind" example:"file"`
	// Version identifies the current content; send it as if_match when
	// overwriting. Empty for sites uploaded before versions existed.
	Version     string `json:"version" example:"5d41402abc4b2a76b9719d911017c592"`
	ContentType string `json:"content_type,omitempty" example:"text/html; charset=utf-8"`
	Filename    string `json:"filename,omitempty" example:"q3.html"`
	OwnerEmail  string `json:"owner_email,omitempty" example:"alex@example.com"`
	FileCount   int    `json:"file_count,omitempty" example:"12"`
	URL         string `json:"url" example:"https://trove.example.com/q3-review"`
}

var errArtifactNotFound = errors.New("artifact not found")

// lookupArtifact returns an artifact's metadata, or errArtifactNotFound.
func (s *server) lookupArtifact(ctx context.Context, slug string) (ArtifactResponse, error) {
	if isInternalSlug(slug) || validateSlug(slug) != nil {
		return ArtifactResponse{}, errArtifactNotFound
	}
	meta, err := s.store.Metadata(ctx, slug)
	if err == nil {
		return ArtifactResponse{
			Slug: slug, Kind: "file", Version: meta.Version, ContentType: meta.ContentType,
			Filename: meta.Filename, OwnerEmail: meta.OwnerEmail, URL: s.viewURLFor(slug),
		}, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return ArtifactResponse{}, err
	}
	manifest, err := s.store.GetSiteManifest(ctx, slug)
	if errors.Is(err, storage.ErrNotFound) {
		return ArtifactResponse{}, errArtifactNotFound
	}
	if err != nil {
		return ArtifactResponse{}, err
	}
	return ArtifactResponse{
		Slug: slug, Kind: "site", Version: manifest.Version, OwnerEmail: manifest.OwnerEmail,
		FileCount: manifest.FileCount, URL: s.viewURLFor(slug),
	}, nil
}

// handleGetArtifact godoc
// @Summary Get artifact metadata
// @Description Returns an artifact's kind, current version, type and owner without its content. Read the version before overwriting and send it as if_match so a concurrent edit isn't lost.
// @Tags files
// @Produce json
// @Param slug path string true "Artifact slug"
// @Success 200 {object} main.ArtifactResponse
// @Failure 404 {object} main.ErrorResponse
// @Failure 500 {object} main.ErrorResponse
// @Router /api/artifacts/{slug} [get]
func (s *server) handleGetArtifact(c echo.Context) error {
	slug := c.Param("slug")
	a, err := s.lookupArtifact(c.Request().Context(), slug)
	if errors.Is(err, errArtifactNotFound) {
		return c.JSON(http.StatusNotFound, ErrorResponse{Error: "artifact not found"})
	}
	if err != nil {
		log.Printf("ERROR artifact metadata slug=%s: %v", slug, err)
		return c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
	}
	if a.Version != "" {
		c.Response().Header().Set("ETag", strconv.Quote(a.Version))
	}
	return c.JSON(http.StatusOK, a)
}
