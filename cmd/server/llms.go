package main

import (
	"crypto/sha256"
	"log"
	"net/http"
	"strings"
	"time"

	trovedocs "github.com/gametimesf/open-trove/docs"
	"github.com/labstack/echo/v4"
)

// registerLLMsTxtRoute godoc
// @Summary LLM-friendly API documentation
// @Description Returns plain-text API documentation for LLM and agent consumption
// @Tags discovery
// @Produce plain
// @Success 200 {string} string "Plain-text API docs"
// @Router /llms.txt [get]
func registerLLMsTxtRoute(e *echo.Echo, override *string) {
	if override == nil {
		e.FileFS("/llms.txt", "llms.txt", trovedocs.Files)
		e.HEAD("/llms.txt", echo.StaticFileHandler("llms.txt", trovedocs.Files))
		log.Printf("INFO llms.txt: source=embedded")
		return
	}

	// Capture immutable startup content. ServeContent retains file-like GET/HEAD
	// and range semantics without touching the filesystem or fetching a URL.
	content := *override
	handler := func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextPlainCharsetUTF8)
		http.ServeContent(c.Response(), c.Request(), "llms.txt", time.Time{}, strings.NewReader(content))
		return nil
	}
	e.GET("/llms.txt", handler)
	e.HEAD("/llms.txt", handler)
	log.Printf("INFO llms.txt: source=override bytes=%d sha256=%x", len(content), sha256.Sum256([]byte(content)))
}
