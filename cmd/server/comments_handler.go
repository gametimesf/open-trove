package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/storage"
	"github.com/labstack/echo/v4"
)

const maxCommentRequestBytes = 64 << 10

// CommentListResponse is the current page's thread collection and storage
// version. The browser uses CurrentVersion to identify stale anchors.
type CommentListResponse struct {
	Threads         []comments.Thread `json:"threads"`
	OpenThreadCount int               `json:"open_thread_count"`
	CurrentVersion  string            `json:"current_version,omitempty"`
	Resource        string            `json:"resource,omitempty"`
}

// handleListComments godoc
// @Summary List artifact comment threads
// @Description Lists whole-artifact threads and threads anchored to the requested site page. Open threads are returned by default.
// @Tags comments
// @Produce json
// @Param X-Trove-User-Email header string false "Email used for audit attribution; send on reads when available"
// @Param slug path string true "Artifact slug"
// @Param path query string false "Page path within a multi-page site"
// @Param resolved query string false "Resolution filter: open, include, or only"
// @Success 200 {object} main.CommentListResponse
// @Failure 400 {object} main.ErrorResponse
// @Failure 404 {object} main.ErrorResponse
// @Failure 500 {object} main.ErrorResponse
// @Router /api/artifacts/{slug}/comments [get]
func (s *server) handleListComments(c echo.Context) error {
	slug := c.Param("slug")
	resource, version, status, err := s.commentArtifactContext(c.Request().Context(), slug, c.QueryParam("path"))
	if err != nil {
		return c.JSON(status, map[string]string{"error": err.Error()})
	}
	resolution, err := parseResolutionFilter(c.QueryParam("resolved"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	threads, err := s.comments.List(c.Request().Context(), slug, resource, resolution)
	if err != nil {
		if errors.Is(err, comments.ErrInvalidComment) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		log.Printf("ERROR listing comment threads slug=%s resource=%q: %v", slug, resource, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	if threads == nil {
		threads = []comments.Thread{}
	}
	openCount := 0
	for _, thread := range threads {
		if thread.Root.ResolvedAt == nil {
			openCount++
		}
	}

	log.Printf("INFO  user_email=%q listed comment threads slug=%s resource=%q count=%d", userEmail(c), slug, resource, len(threads))
	return c.JSON(http.StatusOK, CommentListResponse{
		Threads:         threads,
		OpenThreadCount: openCount,
		CurrentVersion:  version,
		Resource:        resource,
	})
}

// handleCreateComment godoc
// @Summary Create an artifact comment thread
// @Description Creates a root comment attributed from X-Trove-User-Email. Supports whole-file, element, and text anchors.
// @Tags comments
// @Accept json
// @Produce json
// @Param X-Trove-User-Email header string true "Email used for audit attribution"
// @Param slug path string true "Artifact slug"
// @Param comment body comments.CreateInput true "Comment body and anchor"
// @Success 201 {object} comments.Comment
// @Failure 400 {object} main.ErrorResponse
// @Failure 404 {object} main.ErrorResponse
// @Failure 500 {object} main.ErrorResponse
// @Router /api/artifacts/{slug}/comments [post]
func (s *server) handleCreateComment(c echo.Context) error {
	var input comments.CreateInput
	if err := decodeCommentJSON(c, &input); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	slug := c.Param("slug")
	resource, version, status, err := s.commentArtifactContext(c.Request().Context(), slug, input.Anchor.Resource)
	if err != nil {
		return c.JSON(status, map[string]string{"error": err.Error()})
	}
	input.Anchor.Resource = resource

	comment, err := s.comments.Create(c.Request().Context(), slug, userEmail(c), version, input)
	if err != nil {
		return s.handleCommentError(c, "creating root comment", err)
	}
	logCommentMutation(c, "created", comment)
	return c.JSON(http.StatusCreated, comment)
}

// handleReplyComment godoc
// @Summary Reply to an artifact comment thread
// @Description Adds a reply to an open thread.
// @Tags comments
// @Accept json
// @Produce json
// @Param X-Trove-User-Email header string true "Email used for audit attribution"
// @Param slug path string true "Artifact slug"
// @Param comment_id path string true "Root comment ID"
// @Param path query string false "Page path within a multi-page site"
// @Param reply body comments.ReplyInput true "Reply body"
// @Success 201 {object} comments.Comment
// @Failure 400 {object} main.ErrorResponse
// @Failure 404 {object} main.ErrorResponse
// @Failure 409 {object} main.ErrorResponse
// @Failure 500 {object} main.ErrorResponse
// @Router /api/artifacts/{slug}/comments/{comment_id}/replies [post]
func (s *server) handleReplyComment(c echo.Context) error {
	var input comments.ReplyInput
	if err := decodeCommentJSON(c, &input); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	slug := c.Param("slug")
	_, version, status, err := s.commentArtifactContext(c.Request().Context(), slug, c.QueryParam("path"))
	if err != nil {
		return c.JSON(status, map[string]string{"error": err.Error()})
	}
	reply, err := s.comments.Reply(
		c.Request().Context(),
		slug,
		c.Param("comment_id"),
		userEmail(c),
		version,
		input,
	)
	if err != nil {
		return s.handleCommentError(c, "creating reply", err)
	}
	logCommentMutation(c, "replied", reply)
	return c.JSON(http.StatusCreated, reply)
}

// handleEditComment godoc
// @Summary Edit an artifact comment
// @Description Replaces a comment body. The attribution email must match the original author.
// @Tags comments
// @Accept json
// @Produce json
// @Param X-Trove-User-Email header string true "Email used for audit attribution and author matching"
// @Param slug path string true "Artifact slug"
// @Param comment_id path string true "Comment or reply ID"
// @Param path query string false "Page path within a multi-page site"
// @Param edit body comments.EditInput true "Replacement body"
// @Success 200 {object} comments.Comment
// @Failure 400 {object} main.ErrorResponse
// @Failure 403 {object} main.ErrorResponse
// @Failure 404 {object} main.ErrorResponse
// @Failure 409 {object} main.ErrorResponse
// @Failure 500 {object} main.ErrorResponse
// @Router /api/artifacts/{slug}/comments/{comment_id} [patch]
func (s *server) handleEditComment(c echo.Context) error {
	var input comments.EditInput
	if err := decodeCommentJSON(c, &input); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	slug := c.Param("slug")
	if _, _, status, err := s.commentArtifactContext(c.Request().Context(), slug, c.QueryParam("path")); err != nil {
		return c.JSON(status, map[string]string{"error": err.Error()})
	}
	comment, err := s.comments.Edit(c.Request().Context(), slug, c.Param("comment_id"), userEmail(c), input)
	if err != nil {
		return s.handleCommentError(c, "editing comment", err)
	}
	logCommentMutation(c, "edited", comment)
	return c.JSON(http.StatusOK, comment)
}

// handleDeleteComment godoc
// @Summary Delete an artifact comment
// @Description Deletes a comment body while preserving a root tombstone when replies remain. The attribution email must match the original author.
// @Tags comments
// @Produce json
// @Param X-Trove-User-Email header string true "Email used for audit attribution and author matching"
// @Param slug path string true "Artifact slug"
// @Param comment_id path string true "Comment or reply ID"
// @Param path query string false "Page path within a multi-page site"
// @Success 204
// @Failure 400 {object} main.ErrorResponse
// @Failure 403 {object} main.ErrorResponse
// @Failure 404 {object} main.ErrorResponse
// @Failure 409 {object} main.ErrorResponse
// @Failure 500 {object} main.ErrorResponse
// @Router /api/artifacts/{slug}/comments/{comment_id} [delete]
func (s *server) handleDeleteComment(c echo.Context) error {
	slug := c.Param("slug")
	if _, _, status, err := s.commentArtifactContext(c.Request().Context(), slug, c.QueryParam("path")); err != nil {
		return c.JSON(status, map[string]string{"error": err.Error()})
	}
	comment, err := s.comments.Delete(c.Request().Context(), slug, c.Param("comment_id"), userEmail(c))
	if err != nil {
		return s.handleCommentError(c, "deleting comment", err)
	}
	logCommentMutation(c, "deleted", comment)
	return c.NoContent(http.StatusNoContent)
}

// handleResolveCommentThread godoc
// @Summary Resolve or reopen an artifact comment thread
// @Description Updates the resolution state of a root comment thread.
// @Tags comments
// @Accept json
// @Produce json
// @Param X-Trove-User-Email header string true "Email used for audit attribution"
// @Param slug path string true "Artifact slug"
// @Param comment_id path string true "Root comment ID"
// @Param path query string false "Page path within a multi-page site"
// @Param resolution body comments.ResolveInput true "Desired resolution state"
// @Success 200 {object} comments.Comment
// @Failure 400 {object} main.ErrorResponse
// @Failure 404 {object} main.ErrorResponse
// @Failure 409 {object} main.ErrorResponse
// @Failure 500 {object} main.ErrorResponse
// @Router /api/artifacts/{slug}/comments/{comment_id}/resolution [patch]
func (s *server) handleResolveCommentThread(c echo.Context) error {
	var input comments.ResolveInput
	if err := decodeCommentJSON(c, &input); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	slug := c.Param("slug")
	if _, _, status, err := s.commentArtifactContext(c.Request().Context(), slug, c.QueryParam("path")); err != nil {
		return c.JSON(status, map[string]string{"error": err.Error()})
	}
	root, err := s.comments.Resolve(c.Request().Context(), slug, c.Param("comment_id"), userEmail(c), input)
	if err != nil {
		return s.handleCommentError(c, "updating thread resolution", err)
	}
	action := "reopened"
	if input.Resolved {
		action = "resolved"
	}
	logCommentMutation(c, action, root)
	return c.JSON(http.StatusOK, root)
}

func (s *server) handleCommentError(c echo.Context, operation string, err error) error {
	switch {
	case errors.Is(err, comments.ErrInvalidComment):
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, comments.ErrCommentForbidden):
		return c.JSON(http.StatusForbidden, map[string]string{"error": "only the attributed author can edit or delete this comment"})
	case errors.Is(err, comments.ErrCommentNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": "comment not found"})
	case errors.Is(err, comments.ErrThreadResolved):
		return c.JSON(http.StatusConflict, map[string]string{"error": "thread is resolved"})
	case errors.Is(err, comments.ErrCommentDeleted):
		return c.JSON(http.StatusConflict, map[string]string{"error": "comment is deleted"})
	default:
		log.Printf("ERROR %s: %v", operation, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func logCommentMutation(c echo.Context, action string, comment comments.Comment) {
	log.Printf(
		"INFO  user_email=%q %s comment id=%s thread_id=%s slug=%s resource=%q",
		userEmail(c),
		action,
		comment.ID,
		comment.ThreadID,
		comment.Slug,
		comment.Anchor.Resource,
	)
}

func decodeCommentJSON(c echo.Context, input any) error {
	body := http.MaxBytesReader(c.Response(), c.Request().Body, maxCommentRequestBytes)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return fmt.Errorf("invalid comment payload: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("invalid comment payload: multiple JSON values")
		}
		return fmt.Errorf("invalid comment payload: %w", err)
	}
	return nil
}

func parseResolutionFilter(value string) (comments.ResolutionFilter, error) {
	switch comments.ResolutionFilter(strings.TrimSpace(value)) {
	case "", comments.ResolutionOpen:
		return comments.ResolutionOpen, nil
	case comments.ResolutionInclude:
		return comments.ResolutionInclude, nil
	case comments.ResolutionOnly:
		return comments.ResolutionOnly, nil
	default:
		return "", errors.New("resolved must be open, include, or only")
	}
}

// commentArtifactContext validates the slug/resource against existing content
// and returns the server-sourced storage version for anchor drift detection.
func (s *server) commentArtifactContext(ctx context.Context, slug, requestedResource string) (resource, version string, status int, err error) {
	if isInternalSlug(slug) || validateSlug(slug) != nil {
		return "", "", http.StatusNotFound, errors.New("artifact not found")
	}

	meta, metadataErr := s.store.Metadata(ctx, slug)
	if metadataErr == nil {
		return "", meta.Version, http.StatusOK, nil
	}
	if !errors.Is(metadataErr, storage.ErrNotFound) {
		return "", "", http.StatusInternalServerError, errors.New("internal error")
	}

	isSite, headErr := s.store.HeadSite(ctx, slug)
	if headErr != nil {
		return "", "", http.StatusInternalServerError, errors.New("internal error")
	}
	if !isSite {
		return "", "", http.StatusNotFound, errors.New("artifact not found")
	}

	resource, err = normalizeCommentResource(requestedResource)
	if err != nil {
		return "", "", http.StatusBadRequest, err
	}
	if resource == "" {
		resource = "index.html"
	}
	siteMeta, headFileErr := s.store.HeadSiteFile(ctx, slug, resource)
	if headFileErr != nil {
		if errors.Is(headFileErr, storage.ErrNotFound) {
			return "", "", http.StatusNotFound, errors.New("site page not found")
		}
		return "", "", http.StatusInternalServerError, errors.New("internal error")
	}
	return resource, siteMeta.Version, http.StatusOK, nil
}

func normalizeCommentResource(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "/"))
	if value == "" {
		return "", nil
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("invalid site page path")
	}
	if len(cleaned) > 2048 {
		return "", errors.New("site page path is too long")
	}
	return cleaned, nil
}
