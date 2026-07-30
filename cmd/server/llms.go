package main

import (
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
func registerLLMsTxtRoute(e *echo.Echo) {
	e.FileFS("/llms.txt", "llms.txt", trovedocs.Files)
}
