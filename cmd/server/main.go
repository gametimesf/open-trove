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
	"github.com/gametimesf/open-trove/comments"
	"github.com/gametimesf/open-trove/intake"
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

	inspector, failMode, err := intake.BuildInspector(cfg)
	if err != nil {
		log.Fatalf("initializing upload intake: %v", err)
	}

	srv := &server{
		store:      store,
		comments:   comments.NewService(store),
		baseURL:    cfg.BaseURL,
		intake:     inspector,
		intakeFail: failMode,
		uploads: uploadLimits{
			maxBytes:         cfg.Uploads.MaxBytes,
			maxSiteFiles:     cfg.Uploads.MaxSiteFiles,
			maxSiteBytes:     cfg.Uploads.MaxSiteBytes,
			maxSiteFileBytes: cfg.Uploads.MaxSiteFileBytes,
		},
		contentReview: contentReview{
			contactName:  cfg.ContentReview.ContactName,
			contactEmail: cfg.ContentReview.ContactEmail,
		},
	}
	for _, rule := range cfg.ShareURLRules {
		srv.shareURLRules = append(srv.shareURLRules, shareURLRule{
			slugPrefix: rule.SlugPrefix,
			baseURL:    rule.BaseURL,
			pathPrefix: rule.PathPrefix,
		})
	}

	e := echo.New()
	e.HideBanner = true

	registerRoutes(e, srv)

	// MCP endpoint — exposes API routes as Model Context Protocol tools
	mcp := mcpserver.NewWithConfig(e, &mcpserver.Config{
		EnableSwaggerSchemas: true,
	})
	// echo-mcp identifies tools by method and path, so the comment GET and POST
	// routes can coexist. Internal browser assets are not tools.
	mcp.ExcludeEndpoints([]string{
		"/healthz",
		"/swagger/*",
		"/_trove/*",
		"/mcp",
		"/delete/*",
	})
	if err := mcp.Mount("/mcp"); err != nil {
		log.Fatalf("mounting MCP server: %v", err)
	}

	log.Printf("Starting trove on :%s (base URL: %s)", cfg.Port, cfg.BaseURL)
	if err := e.Start(":" + cfg.Port); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func registerRoutes(e *echo.Echo, srv *server) {
	e.Use(requestIdentityMiddleware())

	// Public routes
	e.GET("/healthz", healthHandler)
	e.GET("/identity.js", handleUserIdentityJS)
	registerCommentAssets(e)
	registerLLMsTxtRoute(e)
	e.GET("/.well-known/agent.json", srv.handleAgentJSON)
	e.GET("/swagger/*", echoSwagger.WrapHandler)
	e.File("/openapi.json", "docs/openapi.json")

	// Artifact comments API.
	e.GET("/api/artifacts/:slug/comments", srv.handleListComments)
	e.POST("/api/artifacts/:slug/comments", srv.handleCreateComment)
	e.POST("/api/artifacts/:slug/comments/:comment_id/replies", srv.handleReplyComment)
	e.PATCH("/api/artifacts/:slug/comments/:comment_id", srv.handleEditComment)
	e.DELETE("/api/artifacts/:slug/comments/:comment_id", srv.handleDeleteComment)
	e.PATCH("/api/artifacts/:slug/comments/:comment_id/resolution", srv.handleResolveCommentThread)

	// User-tracked routes
	e.GET("/", srv.handleIndex, userIDMiddleware)
	e.POST("/upload", srv.handleUpload, userIDMiddleware)
	e.GET("/mine", srv.handleMine, userIDMiddleware)
	e.DELETE("/delete/:slug", srv.handleDelete)
	e.GET("/:slug/raw", srv.handleRaw)
	e.GET("/:slug", srv.handleView, userIDMiddleware)
	e.GET("/:slug/*", srv.handleSiteAsset)
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
