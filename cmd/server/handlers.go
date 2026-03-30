package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gametimesf/open-trove/storage"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type server struct {
	store   storage.Store
	baseURL string
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

// handleLLMsTxt godoc
// @Summary LLM-friendly API documentation
// @Description Returns plain-text API documentation for LLM and agent consumption
// @Tags discovery
// @Produce plain
// @Success 200 {string} string "Plain-text API docs"
// @Router /llms.txt [get]
func (s *server) handleLLMsTxt(c echo.Context) error {
	return c.String(http.StatusOK, fmt.Sprintf(`# Trove
> File sharing service. Upload any file, get a shareable link.

## API

Base URL: %s

### Upload a file
POST /upload
Content-Type: multipart/form-data

Parameters:
- file (required): The file to upload
- slug (optional): Custom URL slug (lowercase alphanumeric and hyphens, 1-64 chars)
- overwrite (optional): Set to "true" to replace an existing file at the same slug

Response (JSON):
- url: The shareable URL for the uploaded file
- slug: The slug assigned to the file

Example:
  curl -X POST %s/upload -F file=@report.html -F slug=my-report

### View a file
GET /{slug}
Returns an HTML page that renders the file (markdown, code, images, CSV, HTML, etc.)

### Download raw file
GET /{slug}/raw
Returns the raw file content with its original content type.

### Agent metadata
GET /.well-known/agent.json
Returns structured JSON metadata for agent integration.
`, s.baseURL, s.baseURL))
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
					{"name": "file", "type": "file", "required": true, "description": "The file to upload"},
					{"name": "slug", "type": "string", "required": false, "description": "Custom URL slug (lowercase alphanumeric and hyphens, 1-64 chars)"},
					{"name": "overwrite", "type": "string", "required": false, "description": "Set to 'true' to replace an existing file at the same slug"},
				},
				"example":          fmt.Sprintf("curl -X POST %s/upload -F file=@report.html -F slug=my-report", s.baseURL),
				"response_example": map[string]string{"url": s.baseURL + "/my-report", "slug": "my-report"},
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

	// 200MB max
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "file too large or invalid multipart form"})
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

	// Detect content type: extension first, then sniff, then multipart header
	contentType := detectContentType(header.Filename, file, header.Header.Get("Content-Type"))

	// Reset reader after sniffing
	if seeker, ok := file.(io.Seeker); ok {
		seeker.Seek(0, io.SeekStart)
	}

	// Retry with new slugs on conflict (only for auto-generated slugs)
	const maxRetries = 5
	for attempt := 0; ; attempt++ {
		if err := s.store.Put(r.Context(), slug, file, contentType, header.Filename, userProvidedSlug, overwrite); err != nil {
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
				// Reset reader for retry
				if seeker, ok := file.(io.Seeker); ok {
					seeker.Seek(0, io.SeekStart)
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
		}
		if err := s.store.RecordUpload(r.Context(), uid, rec); err != nil {
			log.Printf("WARN  recording upload for user %s: %v", uid, err)
		}
	}

	log.Printf("INFO  user %s uploaded slug=%s filename=%q custom_slug=%v content_type=%q", uid, slug, header.Filename, userProvidedSlug, contentType)

	url := s.baseURL + "/" + slug
	return c.JSON(http.StatusOK, map[string]string{
		"url":  url,
		"slug": slug,
	})
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
	log.Printf("INFO  user %s viewing slug=%s filename=%q", uid, slug, meta.Filename)
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
	}{
		Slug:         slug,
		Filename:     meta.Filename,
		ContentType:  meta.ContentType,
		RawURL:       "/" + slug + "/raw",
		ViewMode:     viewMode(meta.ContentType, meta.Filename),
		Language:     highlightLang(meta.Filename),
		BaseURL:      s.baseURL,
		DownloadName: downloadName,
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

// @Summary Delete a file
// @Description Deletes a file by slug. This endpoint is intentionally not exposed via MCP or the UI.
// @Tags admin
// @Param slug path string true "File slug"
// @Success 204 "Deleted"
// @Failure 404 {object} map[string]string "Not found"
// @Failure 500 {object} map[string]string "Internal error"
// @Router /delete/{slug} [delete]
func (s *server) handleDelete(c echo.Context) error {
	slug := c.Param("slug")
	log.Printf("INFO  delete slug=%s", slug)

	if err := s.store.Delete(c.Request().Context(), slug); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
		}
		log.Printf("ERROR deleting object %s: %v", slug, err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
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

func renderError(c echo.Context, code int, message string) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	c.Response().WriteHeader(code)
	return errorTemplate.Execute(c.Response(), struct {
		Code    int
		Message string
	}{code, message})
}
