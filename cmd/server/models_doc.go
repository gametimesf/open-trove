package main

import "github.com/gametimesf/open-trove/preview"

// UploadResponse documents the response from a successful file upload.
type UploadResponse struct {
	URL  string `json:"url" example:"https://trove.example.com/my-report"`
	Slug string `json:"slug" example:"my-report"`
	// Type is "site" for ZIP uploads.
	Type string `json:"type,omitempty" example:"site"`
	// Version identifies the stored content; pass it as if_match on the next
	// overwrite.
	Version string `json:"version,omitempty" example:"5d41402abc4b2a76b9719d911017c592"`
	// Hints suggest how to make the artifact preview better (missing title,
	// summary, stats or card) and flag an outdated client.
	Hints []preview.Hint `json:"hints,omitempty"`
}

// VersionConflictResponse is returned with 412 when if_match is stale.
type VersionConflictResponse struct {
	Error          string `json:"error" example:"artifact changed since version 5d41…; re-read it, merge, and retry with the current version"`
	CurrentVersion string `json:"current_version" example:"7c6a180b36896a0a8c02787eeafb0e4c"`
}

// ErrorResponse documents error responses.
type ErrorResponse struct {
	Error string `json:"error" example:"slug already taken"`
}

// HealthResponse documents the health check response.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// AgentEndpointParam documents a parameter in the agent.json endpoint.
type AgentEndpointParam struct {
	Name        string `json:"name" example:"file"`
	Type        string `json:"type" example:"file"`
	Required    bool   `json:"required" example:"true"`
	Description string `json:"description" example:"The file to upload"`
}

// AgentEndpoint documents an endpoint in the agent.json response.
type AgentEndpoint struct {
	Method          string               `json:"method" example:"POST"`
	Path            string               `json:"path" example:"/upload"`
	Description     string               `json:"description" example:"Upload a file. Returns a shareable URL."`
	ContentType     string               `json:"content_type" example:"multipart/form-data"`
	Parameters      []AgentEndpointParam `json:"parameters"`
	Example         string               `json:"example"`
	ResponseExample UploadResponse       `json:"response_example"`
}

// AgentJSON documents the agent.json discovery response.
type AgentJSON struct {
	Name        string          `json:"name" example:"trove"`
	Description string          `json:"description" example:"File sharing service. Upload any file, get a shareable link."`
	APIBase     string          `json:"api_base" example:"https://trove.example.com"`
	Endpoints   []AgentEndpoint `json:"endpoints"`
}
