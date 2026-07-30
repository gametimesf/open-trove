package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/intake"
	"github.com/gametimesf/open-trove/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type server struct {
	store         storage.Store
	comments      *comments.Service
	baseURL       string
	shareURLRules []shareURLRule
	contentReview contentReview
	uploads       uploadLimits
	intake        intake.Inspector // intake.NoOp{} when feature disabled
	intakeFail    intake.FailMode  // closed (default) or open
}

type shareURLRule struct {
	slugPrefix string
	baseURL    string
	pathPrefix string
}

type contentReview struct {
	contactName  string
	contactEmail string
}

type uploadLimits struct {
	maxBytes         int64
	maxSiteFiles     int
	maxSiteBytes     int64
	maxSiteFileBytes int64
}

func (l uploadLimits) withDefaults() uploadLimits {
	if l.maxBytes <= 0 {
		l.maxBytes = 200 << 20
	}
	if l.maxSiteFiles <= 0 {
		l.maxSiteFiles = 2_000
	}
	if l.maxSiteBytes <= 0 {
		l.maxSiteBytes = 200 << 20
	}
	if l.maxSiteFileBytes <= 0 {
		l.maxSiteFileBytes = 100 << 20
	}
	return l
}

const cookieMaxAge = 10 * 365 * 24 * 60 * 60 // ~10 years in seconds

func userIDMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		var uid string
		cookie, err := c.Cookie("trove_id")
		if err == nil && cookie.Value != "" {
			if _, parseErr := uuid.Parse(cookie.Value); parseErr == nil {
				uid = cookie.Value
			}
		}
		if uid == "" {
			uid = uuid.New().String()
			log.Printf("INFO  new user %s", uid)
			c.SetCookie(&http.Cookie{
				Name:     "trove_id",
				Value:    uid,
				Path:     "/",
				MaxAge:   cookieMaxAge,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}
		c.Set("trove_id", uid)
		return next(c)
	}
}

func userID(c echo.Context) string {
	if v, ok := c.Get("trove_id").(string); ok {
		return v
	}
	return ""
}

// Extension → highlight.js language identifier for syntax highlighting.
var extHighlightLangs = map[string]string{
	".go": "go", ".py": "python", ".js": "javascript", ".ts": "typescript",
	".jsx": "javascript", ".tsx": "typescript", ".json": "json", ".css": "css",
	".xml": "xml", ".html": "html", ".htm": "html", ".md": "markdown",
	".yaml": "yaml", ".yml": "yaml", ".toml": "toml", ".sh": "bash",
	".bash": "bash", ".zsh": "bash", ".sql": "sql", ".rs": "rust",
	".java": "java", ".rb": "ruby", ".php": "php", ".c": "c", ".h": "c",
	".cpp": "cpp", ".hpp": "cpp", ".cs": "csharp", ".swift": "swift",
	".kt": "kotlin", ".scala": "scala", ".r": "r", ".lua": "lua",
	".pl": "perl", ".tf": "hcl", ".hcl": "hcl",
	".txt": "plaintext", ".log": "plaintext", ".env": "plaintext",
	".ini": "ini", ".conf": "ini",
}

func highlightLang(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if lang, ok := extHighlightLangs[ext]; ok {
		return lang
	}
	base := strings.ToLower(filepath.Base(filename))
	switch base {
	case "makefile":
		return "makefile"
	case "dockerfile":
		return "dockerfile"
	}
	return ""
}

// Extension-based content type map for types that http.DetectContentType gets wrong.
var extContentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".htm":  "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "application/javascript; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".svg":  "image/svg+xml",
	".pdf":  "application/pdf",
	".xml":  "application/xml; charset=utf-8",
	".csv":  "text/csv; charset=utf-8",
	".md":   "text/markdown; charset=utf-8",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".ogg":  "video/ogg",
	".ogv":  "video/ogg",
	".mkv":  "video/x-matroska",
	".avi":  "video/x-msvideo",
}

// handleAgentJSON godoc
// @Summary Agent discovery metadata
// @Description Returns structured JSON metadata for AI agent integration
// @Tags discovery
// @Produce json
// @Success 200 {object} main.AgentJSON
// @Router /.well-known/agent.json [get]
func (s *server) handleAgentJSON(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"name":        "trove",
		"description": "File sharing service. Upload any file, get a shareable link.",
		"api_base":    s.baseURL,
		"endpoints": []map[string]any{
			{
				"method":       "POST",
				"path":         "/upload",
				"description":  "Upload a file. Returns a shareable URL.",
				"content_type": "multipart/form-data",
				"parameters": []map[string]any{
					{"name": troveUserEmailHeader, "type": "string", "in": "header", "required": true, "description": "Email used for audit attribution; send it on every request and note that it is required for writes"},
					{"name": "file", "type": "file", "required": true, "description": "The file to upload"},
					{"name": "slug", "type": "string", "required": false, "description": "Custom URL slug (lowercase alphanumeric and hyphens, 1-64 chars)"},
					{"name": "overwrite", "type": "string", "required": false, "description": "Set to 'true' to replace an existing file at the same slug"},
				},
				"example":          fmt.Sprintf("curl -X POST %s/upload -H '%s: you@example.com' -F file=@report.html -F slug=my-report", s.baseURL, troveUserEmailHeader),
				"response_example": map[string]string{"url": s.baseURL + "/my-report", "slug": "my-report"},
			},
			{
				"method":      "GET",
				"path":        "/api/artifacts/{slug}/comments",
				"description": "List open comment threads for an artifact or site page; use resolved=include or resolved=only to view resolved threads.",
				"parameters": []map[string]any{
					{"name": troveUserEmailHeader, "type": "string", "in": "header", "required": false, "description": "Email used for audit attribution; send it on reads when available"},
					{"name": "slug", "type": "string", "in": "path", "required": true, "description": "Artifact slug"},
					{"name": "path", "type": "string", "in": "query", "required": false, "description": "Page path within a multi-page site"},
					{"name": "resolved", "type": "string", "in": "query", "required": false, "description": "open, include, or only"},
				},
				"example": fmt.Sprintf("curl %s/api/artifacts/my-report/comments -H '%s: you@example.com'", s.baseURL, troveUserEmailHeader),
			},
			{
				"method":       "POST",
				"path":         "/api/artifacts/{slug}/comments",
				"description":  "Create a whole-file, element, or text comment thread.",
				"content_type": "application/json",
				"parameters": []map[string]any{
					{"name": troveUserEmailHeader, "type": "string", "in": "header", "required": true, "description": "Email used for audit attribution"},
					{"name": "slug", "type": "string", "in": "path", "required": true, "description": "Artifact slug"},
				},
				"example": fmt.Sprintf("curl -X POST %s/api/artifacts/my-report/comments -H '%s: you@example.com' -H 'Content-Type: application/json' -d '{\"body\":\"Clarify this chart\",\"anchor\":{\"type\":\"element\",\"stable_id\":\"revenue-chart\"}}'", s.baseURL, troveUserEmailHeader),
			},
			{
				"method":       "POST",
				"path":         "/api/artifacts/{slug}/comments/{comment_id}/replies",
				"description":  "Reply to an open comment thread. comment_id is the root comment ID.",
				"content_type": "application/json",
				"example":      fmt.Sprintf("curl -X POST %s/api/artifacts/my-report/comments/$ROOT_ID/replies -H '%s: you@example.com' -H 'Content-Type: application/json' -d '{\"body\":\"Updated.\"}'", s.baseURL, troveUserEmailHeader),
			},
			{
				"method":       "PATCH",
				"path":         "/api/artifacts/{slug}/comments/{comment_id}",
				"description":  "Edit a comment or reply. The attribution email must match the original author.",
				"content_type": "application/json",
				"example":      fmt.Sprintf("curl -X PATCH %s/api/artifacts/my-report/comments/$COMMENT_ID -H '%s: you@example.com' -H 'Content-Type: application/json' -d '{\"body\":\"Corrected text.\"}'", s.baseURL, troveUserEmailHeader),
			},
			{
				"method":      "DELETE",
				"path":        "/api/artifacts/{slug}/comments/{comment_id}",
				"description": "Delete a comment or reply. The attribution email must match the original author.",
				"example":     fmt.Sprintf("curl -X DELETE %s/api/artifacts/my-report/comments/$COMMENT_ID -H '%s: you@example.com'", s.baseURL, troveUserEmailHeader),
			},
			{
				"method":       "PATCH",
				"path":         "/api/artifacts/{slug}/comments/{comment_id}/resolution",
				"description":  "Resolve or reopen a root comment thread.",
				"content_type": "application/json",
				"example":      fmt.Sprintf("curl -X PATCH %s/api/artifacts/my-report/comments/$ROOT_ID/resolution -H '%s: you@example.com' -H 'Content-Type: application/json' -d '{\"resolved\":true}'", s.baseURL, troveUserEmailHeader),
			},
		},
	})
}

// handleIndex godoc
// @Summary Upload page
// @Description Renders the file upload page
// @Tags ui
// @Produce html
// @Success 200 {string} string "HTML upload page"
// @Router / [get]
func (s *server) handleIndex(c echo.Context) error {
	return c.HTML(http.StatusOK, uploadPageHTML)
}

// handleUpload godoc
// @Summary Upload a file
// @Description Upload a file and get a shareable link. Supports custom slugs and overwriting existing files.
// @Tags files
// @Accept multipart/form-data
// @Produce json
// @Param X-Trove-User-Email header string true "Email used for audit attribution"
// @Param file formData file true "File to upload"
// @Param slug formData string false "Custom URL slug (lowercase alphanumeric and hyphens, 1-64 chars)"
// @Param overwrite formData string false "Set to 'true' to replace an existing file at the same slug"
// @Success 200 {object} main.UploadResponse
// @Failure 400 {object} main.ErrorResponse
// @Failure 409 {object} main.ErrorResponse "Slug already taken"
// @Failure 500 {object} main.ErrorResponse
// @Router /upload [post]
func (s *server) handleUpload(c echo.Context) error {
	r := c.Request()
	limits := s.uploads.withDefaults()

	// Bound the full request as well as the selected file. The small allowance
	// covers multipart boundaries and scalar fields.
	r.Body = http.MaxBytesReader(c.Response(), r.Body, limits.maxBytes+(1<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "file too large or invalid multipart form"})
	}
	if r.MultipartForm != nil {
		defer func() {
			if err := r.MultipartForm.RemoveAll(); err != nil {
				log.Printf("WARN  cleaning multipart temp files: %v", err)
			}
		}()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no file provided"})
	}
	defer file.Close()

	// Slug: use provided or generate
	slug := c.FormValue("slug")
	userProvidedSlug := slug != ""
	if userProvidedSlug {
		if err := validateSlug(slug); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
	} else {
		slug, err = generateSlug()
		if err != nil {
			log.Printf("ERROR generating slug: %v", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
	}

	overwrite := c.FormValue("overwrite") == "true"

	// Read a bounded copy so intake and storage can safely re-read it.
	fileBytes, err := io.ReadAll(io.LimitReader(file, limits.maxBytes+1))
	if err != nil {
		log.Printf("ERROR reading upload: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	if int64(len(fileBytes)) > limits.maxBytes {
		return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "file too large"})
	}

	// Detect content type: extension first, then sniff, then multipart header
	contentType := detectContentType(header.Filename, bytes.NewReader(fileBytes), header.Header.Get("Content-Type"))

	// Intake gate. ZIP-as-site uploads are inspected per-file inside
	// handleSiteUploadFromZip, so skip the whole-blob check here.
	isZip := strings.HasSuffix(strings.ToLower(header.Filename), ".zip") || contentType == "application/zip" || contentType == "application/x-zip-compressed"
	if !isZip {
		if err := s.gateUpload(c, header.Filename, contentType, fileBytes); err != nil {
			return err
		}
	}

	// If ZIP, treat as a site upload
	if isZip {
		return s.handleSiteUploadFromZip(c, slug, fileBytes, userProvidedSlug, overwrite)
	}

	// Retry with new slugs on conflict (only for auto-generated slugs)
	const maxRetries = 5
	for attempt := 0; ; attempt++ {
		if err := s.store.Put(r.Context(), slug, bytes.NewReader(fileBytes), contentType, header.Filename, userProvidedSlug, overwrite); err != nil {
			if errors.Is(err, storage.ErrSlugConflict) {
				if userProvidedSlug {
					return c.JSON(http.StatusConflict, map[string]string{"error": "slug already taken"})
				}
				if attempt >= maxRetries {
					log.Printf("ERROR slug collision after %d retries", maxRetries)
					return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
				}
				slug, err = generateSlug()
				if err != nil {
					log.Printf("ERROR generating slug: %v", err)
					return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
				}
				continue
			}
			log.Printf("ERROR storing file: %v", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		break
	}

	// Record upload in user manifest (best-effort)
	uid := userID(c)
	if uid != "" {
		rec := storage.ActivityRecord{
			Slug:        slug,
			Filename:    header.Filename,
			ContentType: contentType,
			UserEmail:   userEmail(c),
		}
		if err := s.store.RecordUpload(r.Context(), uid, rec); err != nil {
			log.Printf("WARN  recording upload for user %s: %v", uid, err)
		}
	}

	log.Printf("INFO  user_email=%q user_id=%s uploaded slug=%s filename=%q custom_slug=%v content_type=%q", userEmail(c), uid, slug, header.Filename, userProvidedSlug, contentType)

	url := s.viewURLFor(slug)
	return c.JSON(http.StatusOK, map[string]string{
		"url":  url,
		"slug": slug,
	})
}

// rawURLFor returns the viewer's raw-content URL. Deployments can publish
// selected slug prefixes through an alternate base URL.
func (s *server) rawURLFor(slug string) string {
	if rule, ok := s.shareURLRuleFor(slug); ok {
		return rule.baseURL + rule.pathPrefix + "/" + slug + "/raw"
	}
	return "/" + slug + "/raw"
}

// viewURLFor returns the shareable viewer URL reported to the uploader.
func (s *server) viewURLFor(slug string) string {
	if rule, ok := s.shareURLRuleFor(slug); ok {
		return rule.baseURL + rule.pathPrefix + "/" + slug
	}
	return s.baseURL + "/" + slug
}

func (s *server) shareURLRuleFor(slug string) (shareURLRule, bool) {
	for _, rule := range s.shareURLRules {
		if strings.HasPrefix(slug, rule.slugPrefix) {
			return rule, true
		}
	}
	return shareURLRule{}, false
}

// handleView godoc
// @Summary View a file
// @Description Renders an HTML viewer for the uploaded file (markdown, code, images, CSV, HTML, etc.)
// @Tags files
// @Produce html
// @Param slug path string true "File slug"
// @Success 200 {string} string "HTML viewer page"
// @Failure 404 {string} string "File not found"
// @Router /{slug} [get]
func (s *server) handleView(c echo.Context) error {
	slug := c.Param("slug")
	if isInternalSlug(slug) {
		return renderError(c, http.StatusNotFound, "File not found")
	}

	// Check if this slug is a site
	isSite, err := s.store.HeadSite(c.Request().Context(), slug)
	if err != nil {
		log.Printf("ERROR checking site %s: %v", slug, err)
		return renderError(c, http.StatusInternalServerError, "Internal error")
	}
	if isSite {
		uid := userID(c)
		log.Printf("INFO  user_email=%q user_id=%s viewing site slug=%s", userEmail(c), uid, slug)
		c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
		return siteViewerTemplate.Execute(c.Response(), struct {
			Slug    string
			BaseURL string
		}{Slug: slug, BaseURL: s.baseURL})
	}

	meta, err := s.store.Metadata(c.Request().Context(), slug)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return renderError(c, http.StatusNotFound, "File not found")
		}
		log.Printf("ERROR heading object %s: %v", slug, err)
		return renderError(c, http.StatusInternalServerError, "Internal error")
	}

	// Record view in user manifest (best-effort, skip own uploads)
	uid := userID(c)
	log.Printf("INFO  user_email=%q user_id=%s viewing slug=%s filename=%q", userEmail(c), uid, slug, meta.Filename)
	if uid != "" {
		manifest, err := s.store.GetManifest(c.Request().Context(), uid)
		isOwnUpload := false
		if err == nil {
			for _, u := range manifest.Uploads {
				if u.Slug == slug {
					isOwnUpload = true
					break
				}
			}
		}
		if !isOwnUpload {
			rec := storage.ActivityRecord{
				Slug:        slug,
				Filename:    meta.Filename,
				ContentType: meta.ContentType,
				UserEmail:   userEmail(c),
			}
			if err := s.store.RecordView(c.Request().Context(), uid, rec); err != nil {
				log.Printf("WARN recording view for user %s: %v", uid, err)
			}
		}
	}

	downloadName := meta.Filename
	if meta.CustomSlug {
		downloadName = slug + filepath.Ext(meta.Filename)
	}

	data := struct {
		Slug         string
		Filename     string
		ContentType  string
		RawURL       string
		ViewMode     string
		Language     string
		BaseURL      string
		DownloadName string
		Flagged      bool
		FlagReason   string
		ReviewName   string
		ReviewMailto string
	}{
		Slug:         slug,
		Filename:     meta.Filename,
		ContentType:  meta.ContentType,
		Flagged:      meta.Flagged,
		FlagReason:   meta.FlagReason,
		RawURL:       s.rawURLFor(slug),
		ViewMode:     viewMode(meta.ContentType, meta.Filename),
		Language:     highlightLang(meta.Filename),
		BaseURL:      s.baseURL,
		DownloadName: downloadName,
		ReviewName:   s.reviewContactName(),
		ReviewMailto: s.reviewMailto(slug, meta.FlagReason),
	}

	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	return viewerTemplate.Execute(c.Response(), data)
}

// handleMine godoc
// @Summary User activity dashboard
// @Description Shows the current user's upload and view history
// @Tags ui
// @Produce html
// @Success 200 {string} string "HTML activity page"
// @Router /mine [get]
func (s *server) handleMine(c echo.Context) error {
	uid := userID(c)
	log.Printf("INFO  user %s accessing /mine", uid)
	if uid == "" {
		c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
		return myTroveTemplate.Execute(c.Response(), &storage.UserManifest{})
	}

	manifest, err := s.store.GetManifest(c.Request().Context(), uid)
	if err != nil {
		log.Printf("ERROR getting manifest for user %s: %v", uid, err)
		manifest = &storage.UserManifest{}
	}

	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	return myTroveTemplate.Execute(c.Response(), manifest)
}

// handleRaw godoc
// @Summary Download raw file
// @Description Returns the raw file content with its original content type. Supports Range requests.
// @Tags files
// @Param slug path string true "File slug"
// @Param Range header string false "HTTP Range header for partial content"
// @Success 200 {file} binary "File content"
// @Success 206 {file} binary "Partial file content"
// @Failure 404 {string} string "File not found"
// @Router /{slug}/raw [get]
func (s *server) handleRaw(c echo.Context) error {
	slug := c.Param("slug")
	if isInternalSlug(slug) {
		return renderError(c, http.StatusNotFound, "File not found")
	}
	log.Printf("INFO  raw download slug=%s", slug)

	rangeHeader := c.Request().Header.Get("Range")
	body, meta, err := s.store.Get(c.Request().Context(), slug, rangeHeader)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return renderError(c, http.StatusNotFound, "File not found")
		}
		log.Printf("ERROR getting object %s: %v", slug, err)
		return renderError(c, http.StatusInternalServerError, "Internal error")
	}
	defer body.Close()

	c.Response().Header().Set(echo.HeaderContentType, meta.ContentType)
	c.Response().Header().Set("Accept-Ranges", "bytes")
	if meta.Filename != "" {
		downloadName := meta.Filename
		if meta.CustomSlug {
			downloadName = slug + filepath.Ext(meta.Filename)
		}
		disp := mime.FormatMediaType("inline", map[string]string{"filename": downloadName})
		c.Response().Header().Set("Content-Disposition", disp)
	}
	if meta.ContentRange != "" {
		c.Response().Header().Set("Content-Range", meta.ContentRange)
		c.Response().Header().Set(echo.HeaderContentLength, fmt.Sprintf("%d", meta.ContentLength))
		c.Response().WriteHeader(http.StatusPartialContent)
	}
	_, err = io.Copy(c.Response(), body)
	return err
}

// zipSingleDirPrefix returns the directory prefix to strip when all files in a
// ZIP share a single top-level directory (e.g. "reports/" from a macOS folder zip).
// Returns an empty string if files are already at the root.
func zipSingleDirPrefix(files []*zip.File) string {
	var prefix string
	for _, f := range files {
		if strings.HasPrefix(f.Name, "__MACOSX/") {
			continue
		}
		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) < 2 {
			return "" // file at root — no prefix to strip
		}
		dir := parts[0] + "/"
		if prefix == "" {
			prefix = dir
		} else if prefix != dir {
			return "" // multiple top-level directories
		}
	}
	return prefix
}

func (s *server) handleSiteUploadFromZip(c echo.Context, slug string, zipBytes []byte, userProvidedSlug, overwrite bool) error {
	r := c.Request()
	limits := s.uploads.withDefaults()

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid ZIP file"})
	}

	// Detect common top-level directory prefix (e.g. macOS zips a folder as "reports/index.html").
	// If every non-MACOSX file shares a single top-level directory, strip it so files are treated as root-level.
	stripPrefix := zipSingleDirPrefix(zr.File)

	// Validate: must have index.html after stripping prefix
	hasIndex := false
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.HasPrefix(f.Name, "__MACOSX/") {
			continue
		}
		if strings.TrimPrefix(f.Name, stripPrefix) == "index.html" {
			hasIndex = true
			break
		}
	}
	if !hasIndex {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "ZIP must contain index.html"})
	}

	// Check for slug conflict
	if !overwrite {
		if exists, err := s.store.HeadSite(r.Context(), slug); err != nil {
			log.Printf("ERROR checking site slug %s: %v", slug, err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
		} else if exists {
			if userProvidedSlug {
				return c.JSON(http.StatusConflict, map[string]string{"error": "slug already taken"})
			}
			var genErr error
			slug, genErr = generateSlug()
			if genErr != nil {
				log.Printf("ERROR generating slug: %v", genErr)
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
		}
	}

	// Two-pass: read-and-inspect everything before persisting anything.
	// If any file is rejected, we leave the store untouched.
	type pendingFile struct {
		path        string
		contentType string
		data        []byte
	}
	pending := make([]pendingFile, 0, len(zr.File))
	var expandedBytes int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.HasPrefix(f.Name, "__MACOSX/") {
			continue
		}
		if len(pending) >= limits.maxSiteFiles {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "ZIP contains too many files"})
		}
		storedPath, err := safeSitePath(f.Name, stripPrefix)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid ZIP entry %q", f.Name)})
		}
		if f.UncompressedSize64 > uint64(limits.maxSiteFileBytes) {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("ZIP entry %q is too large", f.Name)})
		}
		if f.UncompressedSize64 > uint64(limits.maxSiteBytes-expandedBytes) {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "expanded ZIP is too large"})
		}
		rc, err := f.Open()
		if err != nil {
			log.Printf("ERROR opening zip entry %s: %v", f.Name, err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		data, err := io.ReadAll(io.LimitReader(rc, limits.maxSiteFileBytes+1))
		rc.Close()
		if err != nil {
			log.Printf("ERROR reading zip entry %s: %v", f.Name, err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		if int64(len(data)) > limits.maxSiteFileBytes {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("ZIP entry %q is too large", f.Name)})
		}
		expandedBytes += int64(len(data))
		if expandedBytes > limits.maxSiteBytes {
			return c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": "expanded ZIP is too large"})
		}
		ct := detectContentType(storedPath, bytes.NewReader(data), "")
		if err := s.gateUpload(c, storedPath, ct, data); err != nil {
			// gateUpload already wrote the response. Abort the whole site
			// upload — first violation kills it before anything is stored.
			return err
		}
		pending = append(pending, pendingFile{path: storedPath, contentType: ct, data: data})
	}

	fileCount := 0
	for _, pf := range pending {
		if err := s.store.PutSiteFile(r.Context(), slug, pf.path, bytes.NewReader(pf.data), pf.contentType); err != nil {
			log.Printf("ERROR storing site file %s/%s: %v", slug, pf.path, err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		fileCount++
	}

	if err := s.store.PutSiteManifest(r.Context(), slug, &storage.SiteManifest{Entry: "index.html", FileCount: fileCount}); err != nil {
		log.Printf("ERROR storing site manifest for %s: %v", slug, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	uid := userID(c)
	if uid != "" {
		rec := storage.ActivityRecord{
			Slug:        slug,
			Filename:    slug,
			ContentType: "text/html; charset=utf-8",
			UserEmail:   userEmail(c),
		}
		if err := s.store.RecordUpload(r.Context(), uid, rec); err != nil {
			log.Printf("WARN recording site upload for user %s: %v", uid, err)
		}
	}

	log.Printf("INFO  user_email=%q user_id=%s uploaded site slug=%s files=%d custom_slug=%v", userEmail(c), uid, slug, fileCount, userProvidedSlug)

	return c.JSON(http.StatusOK, map[string]string{
		"url":  s.viewURLFor(slug),
		"slug": slug,
		"type": "site",
	})
}

func safeSitePath(name, stripPrefix string) (string, error) {
	if strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") {
		return "", errors.New("invalid path characters")
	}
	value := strings.TrimPrefix(name, stripPrefix)
	if value == "" || strings.HasPrefix(value, "/") {
		return "", errors.New("empty or absolute path")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("path traversal")
	}
	return cleaned, nil
}

func (s *server) reviewContactName() string {
	if name := strings.TrimSpace(s.contentReview.contactName); name != "" {
		return name
	}
	return "the site administrator"
}

func (s *server) reviewMailto(slug, reason string) string {
	email := strings.TrimSpace(s.contentReview.contactEmail)
	if email == "" {
		return ""
	}
	query := url.Values{}
	query.Set("subject", "Content review needed in Trove: "+slug)
	query.Set("body", fmt.Sprintf("An automated review flagged this Trove artifact. Please review it.\n\nLink: %s/%s\n\nReason: %s\n", s.baseURL, slug, reason))
	return "mailto:" + email + "?" + query.Encode()
}

// handleSiteAsset godoc
// @Summary Serve a site asset
// @Description Returns a file from an uploaded multi-page site
// @Tags files
// @Param slug path string true "Site slug"
// @Param path path string true "File path within the site"
// @Success 200 {file} binary "File content"
// @Failure 404 {string} string "Not found"
// @Router /{slug}/{path} [get]
func (s *server) handleSiteAsset(c echo.Context) error {
	slug := c.Param("slug")
	if isInternalSlug(slug) {
		return renderError(c, http.StatusNotFound, "File not found")
	}
	path := c.Param("*")
	safePath, err := safeSitePath(path, "")
	if err != nil {
		return renderError(c, http.StatusNotFound, "File not found")
	}

	body, meta, err := s.store.GetSiteFile(c.Request().Context(), slug, safePath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return renderError(c, http.StatusNotFound, "File not found")
		}
		log.Printf("ERROR getting site file %s/%s: %v", slug, safePath, err)
		return renderError(c, http.StatusInternalServerError, "Internal error")
	}
	defer body.Close()

	c.Response().Header().Set(echo.HeaderContentType, meta.ContentType)
	_, err = io.Copy(c.Response(), body)
	return err
}

// @Summary Delete a file
// @Description Deletes a file by slug. This endpoint is intentionally not exposed via MCP or the UI.
// @Tags admin
// @Param X-Trove-User-Email header string true "Email used for audit attribution"
// @Param slug path string true "File slug"
// @Success 204 "Deleted"
// @Failure 400 {object} map[string]string "Missing user email"
// @Failure 404 {object} map[string]string "Not found"
// @Failure 500 {object} map[string]string "Internal error"
// @Router /delete/{slug} [delete]
func (s *server) handleDelete(c echo.Context) error {
	slug := c.Param("slug")
	log.Printf("INFO  user_email=%q delete slug=%s", userEmail(c), slug)

	if err := s.store.Delete(c.Request().Context(), slug); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		log.Printf("ERROR deleting object %s: %v", slug, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	if err := s.store.DeleteComments(c.Request().Context(), slug); err != nil {
		// The artifact is already deleted; keep the response truthful while
		// surfacing cleanup failure for repair instead of claiming the file
		// remains available.
		log.Printf("WARN deleting comments for removed object %s: %v", slug, err)
	}

	return c.NoContent(http.StatusNoContent)
}

func detectContentType(filename string, file io.Reader, multipartType string) string {
	// 1. Extension-based lookup
	ext := strings.ToLower(filepath.Ext(filename))
	if ct, ok := extContentTypes[ext]; ok {
		return ct
	}

	// Also check Go's built-in mime types for the extension
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}

	// 2. Sniff first 512 bytes
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	if n > 0 {
		ct := http.DetectContentType(buf[:n])
		if ct != "application/octet-stream" {
			return ct
		}
	}

	// 3. Multipart header
	if multipartType != "" && multipartType != "application/octet-stream" {
		return multipartType
	}

	return "application/octet-stream"
}

func viewMode(contentType, filename string) string {
	switch {
	case strings.HasPrefix(contentType, "text/html"):
		return "iframe"
	case strings.HasPrefix(contentType, "image/"):
		return "image"
	case strings.HasPrefix(contentType, "video/"):
		return "video"
	case strings.HasPrefix(contentType, "text/csv"):
		return "csv"
	case strings.HasPrefix(contentType, "text/markdown"):
		return "markdown"
	case contentType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return "docx"
	case highlightLang(filename) != "":
		return "code"
	default:
		return "download"
	}
}

// errIntakeAborted is the sentinel callers see when gateUpload has
// already written a 403 (blocked) or 503 (fail-closed inspector error)
// to the response. They should propagate the error back through echo,
// which will not double-write because the response is already
// committed.
var errIntakeAborted = errors.New("intake: response already written")

// gateUpload runs the intake inspector against a file and either:
//   - returns nil (allowed → caller proceeds with persisting)
//   - writes a 403 JSON response and returns errIntakeAborted (blocked)
//   - writes a 503 JSON response on inspector failure when fail-closed,
//     also returning errIntakeAborted
//   - returns nil on inspector failure when fail-open (logs a warning)
//
// Callers must propagate the returned error so the handler unwinds
// without persisting anything; the response has already been written
// when the error is non-nil.
func (s *server) gateUpload(c echo.Context, filename, contentType string, body []byte) error {
	if s.intake == nil {
		// Defensive — main wiring guarantees a non-nil inspector (NoOp
		// when disabled), but if a caller constructs a server directly
		// without it, treat as no gate.
		return nil
	}
	verdict, err := s.intake.Inspect(c.Request().Context(), intake.Input{
		Filename:    filename,
		ContentType: contentType,
		Body:        body,
	})
	if err != nil {
		log.Printf("ERROR intake inspection filename=%q content_type=%q: %v", filename, contentType, err)
		if s.intakeFail == intake.FailOpen {
			log.Printf("WARN  intake fail-open: allowing upload despite inspector error filename=%q", filename)
			return nil
		}
		_ = c.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "upload inspection unavailable, please retry",
		})
		return errIntakeAborted
	}
	if !verdict.Allowed {
		log.Printf("INFO  intake blocked filename=%q content_type=%q reason=%q categories=%v",
			filename, contentType, verdict.Reason, verdict.Categories)
		_ = c.JSON(http.StatusForbidden, map[string]any{
			"error":      verdict.Reason,
			"categories": verdict.Categories,
		})
		return errIntakeAborted
	}
	log.Printf("INFO  intake allowed filename=%q content_type=%q", filename, contentType)
	return nil
}

func renderError(c echo.Context, code int, message string) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	c.Response().WriteHeader(code)
	return errorTemplate.Execute(c.Response(), struct {
		Code    int
		Message string
	}{code, message})
}
