package main

// UploadResponse documents the response from a successful file upload.
type UploadResponse struct {
	URL  string `json:"url" example:"http://localhost:8080/my-report"`
	Slug string `json:"slug" example:"my-report"`
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
	APIBase     string          `json:"api_base" example:"http://localhost:8080"`
	Endpoints   []AgentEndpoint `json:"endpoints"`
}
