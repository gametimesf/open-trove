package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/gametimesf/open-trove/preview"
)

// cardFormOverhead allows for multipart framing and the alt field around the image.
const cardFormOverhead = 64 << 10

// cardResponse describes a stored preview card.
type cardResponse struct {
	preview.Card
	URL string `json:"url" example:"https://trove.example.com/api/artifacts/q3-review/preview"`
}

// handlePutCard godoc
// @Summary Set an artifact's preview card
// @Description Stores an author-made preview image (PNG or JPEG, up to 2 MB and 2000x2000; 1200x630 recommended) shown in link previews instead of the generated excerpt. Render it yourself, e.g. a compact HTML card screenshotted with a headless browser. Replaces any existing card.
// @Tags previews
// @Accept multipart/form-data
// @Produce json
// @Param X-Trove-User-Email header string true "Email used for audit attribution"
// @Param slug path string true "Artifact slug"
// @Param image formData file true "PNG or JPEG preview image"
// @Param alt formData string false "Alt text describing the image"
// @Success 200 {object} main.cardResponse
// @Failure 400 {object} main.ErrorResponse
// @Failure 404 {object} main.ErrorResponse
// @Failure 413 {object} main.ErrorResponse
// @Failure 500 {object} main.ErrorResponse
// @Router /api/artifacts/{slug}/preview [put]
func (s *server) handlePutCard(c echo.Context) error {
	slug := c.Param("slug")
	ctx := c.Request().Context()
	_, version, status, err := s.commentArtifactContext(ctx, slug, "")
	if err != nil {
		return c.JSON(status, ErrorResponse{Error: err.Error()})
	}

	req := c.Request()
	req.Body = http.MaxBytesReader(c.Response(), req.Body, preview.MaxCardBytes+cardFormOverhead)
	file, _, err := req.FormFile("image")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return c.JSON(http.StatusRequestEntityTooLarge, ErrorResponse{Error: preview.ErrCardTooLarge.Error()})
		}
		return c.JSON(http.StatusBadRequest, ErrorResponse{Error: "multipart field \"image\" is required"})
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, preview.MaxCardBytes+1))
	if err != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{Error: "reading image"})
	}

	card, err := preview.NewCard(data, req.FormValue("alt"))
	switch {
	case errors.Is(err, preview.ErrCardTooLarge):
		return c.JSON(http.StatusRequestEntityTooLarge, ErrorResponse{Error: err.Error()})
	case errors.Is(err, preview.ErrCardFormat), errors.Is(err, preview.ErrCardDimensions):
		return c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
	case err != nil:
		log.Printf("ERROR preview card encode slug=%s: %v", slug, err)
		return c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
	}
	card.ArtifactVersion = version
	card.UpdatedAt = time.Now().UTC()
	card.UpdatedByEmail = userEmail(c)

	if err := s.store.PutCard(ctx, slug, card); err != nil {
		log.Printf("ERROR preview card store slug=%s: %v", slug, err)
		return c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
	}
	log.Printf("INFO  user_email=%q set preview card slug=%s sha256=%s size=%dx%d bytes=%d", card.UpdatedByEmail, slug, card.SHA256, card.Width, card.Height, len(card.Image))
	return c.JSON(http.StatusOK, s.cardResponse(slug, card))
}

// handleGetCard godoc
// @Summary Get an artifact's preview card image
// @Tags previews
// @Produce png
// @Produce jpeg
// @Param slug path string true "Artifact slug"
// @Success 200 {file} binary
// @Failure 404 {object} main.ErrorResponse
// @Router /api/artifacts/{slug}/preview [get]
func (s *server) handleGetCard(c echo.Context) error {
	slug := c.Param("slug")
	if isInternalSlug(slug) || validateSlug(slug) != nil {
		return c.JSON(http.StatusNotFound, ErrorResponse{Error: "preview card not found"})
	}
	card, err := s.store.GetCard(c.Request().Context(), slug)
	if errors.Is(err, preview.ErrCardNotFound) {
		return c.JSON(http.StatusNotFound, ErrorResponse{Error: "preview card not found"})
	}
	if err != nil {
		log.Printf("ERROR preview card read slug=%s: %v", slug, err)
		return c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
	}
	h := c.Response().Header()
	h.Set("ETag", strconv.Quote(card.SHA256))
	h.Set("Cache-Control", "private, max-age=60")
	h.Set("X-Content-Type-Options", "nosniff")
	return c.Blob(http.StatusOK, card.ContentType, card.Image)
}

// handleDeleteCard godoc
// @Summary Remove an artifact's preview card
// @Description Link previews fall back to the summary generated from the artifact.
// @Tags previews
// @Param X-Trove-User-Email header string true "Email used for audit attribution"
// @Param slug path string true "Artifact slug"
// @Success 204
// @Failure 404 {object} main.ErrorResponse
// @Router /api/artifacts/{slug}/preview [delete]
func (s *server) handleDeleteCard(c echo.Context) error {
	slug := c.Param("slug")
	if isInternalSlug(slug) || validateSlug(slug) != nil {
		return c.JSON(http.StatusNotFound, ErrorResponse{Error: "preview card not found"})
	}
	err := s.store.DeleteCard(c.Request().Context(), slug)
	if errors.Is(err, preview.ErrCardNotFound) {
		return c.JSON(http.StatusNotFound, ErrorResponse{Error: "preview card not found"})
	}
	if err != nil {
		log.Printf("ERROR preview card delete slug=%s: %v", slug, err)
		return c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal error"})
	}
	log.Printf("INFO  user_email=%q deleted preview card slug=%s", userEmail(c), slug)
	return c.NoContent(http.StatusNoContent)
}

func (s *server) cardResponse(slug string, card preview.Card) cardResponse {
	return cardResponse{Card: card, URL: s.baseURL + "/api/artifacts/" + url.PathEscape(slug) + "/preview"}
}
