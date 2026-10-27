package main

import (
	mcpserver "github.com/BrunoKrugel/echo-mcp"
	"github.com/labstack/echo/v4"

	"github.com/gametimesf/open-trove/auth"
)

// mcpForwardHeaders are copied from the /mcp request onto each tool call, so
// tool writes resolve the same verified identity as the MCP client. echo-mcp
// strips them from tool arguments, so an agent can't supply its own.
var mcpForwardHeaders = []string{echo.HeaderAuthorization, auth.CloudflareAccessHeader}

// mountMCP exposes the API routes as Model Context Protocol tools at /mcp.
func mountMCP(e *echo.Echo) error {
	mcp := mcpserver.NewWithConfig(e, &mcpserver.Config{
		EnableSwaggerSchemas: true,
		ForwardHeaders:       mcpForwardHeaders,
	})
	// echo-mcp identifies tools by method and path, so the comment GET and POST
	// routes can coexist. Internal browser assets are not tools.
	mcp.ExcludeEndpoints([]string{
		"/healthz",
		"/favicon.svg",
		"/favicon.png",
		"/apple-touch-icon.png",
		"/api/tokens",
		"/api/tokens/*",
		"/swagger/*",
		"/_trove/*",
		"/mcp",
		"/delete/*",
		// Binary image upload and download don't fit MCP tool calls.
		"/api/artifacts/:slug/preview",
	})
	return mcp.Mount("/mcp")
}
