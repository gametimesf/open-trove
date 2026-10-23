package main

import (
	"embed"

	"github.com/labstack/echo/v4"
)

//go:embed static/comments.css static/comments.js
var commentAssets embed.FS

func registerCommentAssets(e *echo.Echo) {
	e.FileFS("/_trove/comments.css", "static/comments.css", commentAssets)
	e.FileFS("/_trove/comments.js", "static/comments.js", commentAssets)
}
