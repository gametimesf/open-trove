// @title Trove API
// @version 1.0
// @description File sharing service. Upload any file, get a shareable link.
// @host localhost:8080
// @BasePath /
// @schemes https http
package main

import (
	"context"
	"log"
	"net/http"

	mcpserver "github.com/BrunoKrugel/echo-mcp"
	"github.com/gametimesf/open-trove/internal/config"
	"github.com/gametimesf/open-trove/storage"
	"github.com/labstack/echo/v4"
	echoSwagger "github.com/swaggo/echo-swagger"

	_ "github.com/gametimesf/open-trove/docs/swagger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	ctx := context.Background()
	store, err := storage.NewStore(ctx, cfg.Store)
	if err != nil {
		log.Fatalf("initializing storage: %v", err)
	}

	srv := &server{
		store:   store,
		baseURL: cfg.BaseURL,
	}

	e := echo.New()
	e.HideBanner = true

	registerRoutes(e, srv)

	// MCP endpoint — exposes API routes as Model Context Protocol tools
	mcp := mcpserver.NewWithConfig(e, &mcpserver.Config{
		EnableSwaggerSchemas: true,
	})
	// TODO: echo-mcp matches by path only, not method — can't exclude DELETE /:slug
	// while keeping GET /:slug. Workaround: DELETE is at /delete/:slug, excluded from MCP.
	mcp.ExcludeEndpoints([]string{"/healthz", "/swagger/*", "/mcp", "/delete/*"})
	if err := mcp.Mount("/mcp"); err != nil {
		log.Fatalf("mounting MCP server: %v", err)
	}


	log.Printf("Starting trove on :%s (base URL: %s)", cfg.Port, cfg.BaseURL)
	if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func registerRoutes(e *echo.Echo, srv *server) {
	// Public routes
	e.GET("/healthz", healthHandler)
	e.GET("/llms.txt", srv.handleLLMsTxt)
	e.GET("/.well-known/agent.json", srv.handleAgentJSON)
	e.GET("/swagger/*", echoSwagger.WrapHandler)
	e.File("/openapi.json", "docs/openapi.json")

	// User-tracked routes
	e.GET("/", srv.handleIndex, userIDMiddleware)
	e.POST("/upload", srv.handleUpload, userIDMiddleware)
	e.GET("/mine", srv.handleMine, userIDMiddleware)
	e.DELETE("/delete/:slug", srv.handleDelete)
	e.GET("/:slug/raw", srv.handleRaw)
	e.GET("/:slug", srv.handleView, userIDMiddleware)
}

// healthHandler godoc
// @Summary Health check
// @Description Returns service health status
// @Tags health
// @Produce json
// @Success 200 {object} main.HealthResponse
// @Router /healthz [get]
func healthHandler(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
