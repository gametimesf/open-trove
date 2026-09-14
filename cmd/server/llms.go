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
func registerLLMsTxtRoute(e *echo.Echo, override, appendix *string) {
	if override == nil && appendix == nil {
		e.FileFS("/llms.txt", "llms.txt", trovedocs.Files)
		e.HEAD("/llms.txt", echo.StaticFileHandler("llms.txt", trovedocs.Files))
		log.Printf("INFO llms.txt: source=embedded")
		return
	}

	// Capture immutable startup content. ServeContent retains file-like GET/HEAD
	// and range semantics without touching the filesystem or fetching a URL.
	var content, source string
	if appendix != nil {
		embedded, err := trovedocs.Files.ReadFile("llms.txt")
		if err != nil {
			// This is a build invariant: the default guide is compiled into the binary.
			panic("reading embedded llms.txt: " + err.Error())
		}
		content, source = string(embedded)+"\n\n"+*appendix, "embedded+append"
	} else {
		content, source = *override, "override"
	}
	handler := func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextPlainCharsetUTF8)
		http.ServeContent(c.Response(), c.Request(), "llms.txt", time.Time{}, strings.NewReader(content))
		return nil
	}
	e.GET("/llms.txt", handler)
	e.HEAD("/llms.txt", handler)
	log.Printf("INFO llms.txt: source=%s bytes=%d sha256=%x", source, len(content), sha256.Sum256([]byte(content)))
}
