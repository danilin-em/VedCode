package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"VedCode/internal/config"
	"VedCode/internal/providers"
	"VedCode/internal/store"
)

// EmbeddingProvider abstracts the embedding API for search queries.
type EmbeddingProvider interface {
	EmbedContent(text string) ([]float32, error)
}

// Server wraps the MCP server with VedCode-specific tool handlers.
type Server struct {
	mcpServer *server.MCPServer
	store     store.Store
	provider  EmbeddingProvider
	rootPath  string
}

// NewServer creates a new MCP server with all VedCode tools registered.
func NewServer(st store.Store, provider EmbeddingProvider, rootPath string) *Server {
	s := &Server{
		store:    st,
		provider: provider,
		rootPath: rootPath,
	}

	s.mcpServer = server.NewMCPServer(
		"VedCode",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	s.registerTools()
	return s
}

// ServeStdio starts the MCP server using STDIO transport.
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.mcpServer)
}

func (s *Server) registerTools() {
	// search_code
	searchTool := mcp.NewTool("search_code",
		mcp.WithDescription("Semantic search for project files by description. Returns files matching the query with relevance scores."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query describing the code you're looking for (e.g. 'payment processing', 'user authentication')"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return (default: 5)"),
		),
	)
	s.mcpServer.AddTool(searchTool, s.handleSearchCode)

	// get_project_overview
	overviewTool := mcp.NewTool("get_project_overview",
		mcp.WithDescription("Get the project architecture overview. Returns a high-level description of the project structure, frameworks, domains, and patterns."),
	)
	s.mcpServer.AddTool(overviewTool, s.handleGetProjectOverview)

	// get_summary
	summaryTool := mcp.NewTool("get_summary",
		mcp.WithDescription("Get the semantic summary of a specific file by its path. Returns description, responsibilities, and domain."),
		mcp.WithString("file_path",
			mcp.Required(),
			mcp.Description("Path to the file in the project (e.g. 'src/Payment/Gateway.php')"),
		),
	)
	s.mcpServer.AddTool(summaryTool, s.handleGetSummary)
}

func (s *Server) handleSearchCode(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query parameter is required"), nil
	}

	limit := request.GetInt("limit", 5)
	if limit <= 0 {
		limit = 5
	}

	vector, err := s.provider.EmbedContent(query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to embed query: %v", err)), nil
	}

	results, err := s.store.Search(vector, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
	}

	data, err := json.Marshal(results)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal results: %v", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleGetProjectOverview(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	overviewPath := filepath.Join(s.rootPath, ".vedcode", "project_overview.md")

	content, err := os.ReadFile(overviewPath)
	if err != nil {
		if os.IsNotExist(err) {
			return mcp.NewToolResultError("project is not indexed yet; run 'vedcode indexer' first"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("failed to read project overview: %v", err)), nil
	}

	resp := map[string]string{"overview": string(content)}
	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal response: %v", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// RunServer loads config, initializes dependencies, and starts the MCP server.
func RunServer(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	rootPath, err := filepath.Abs(cfg.Project.RootPath)
	if err != nil {
		return fmt.Errorf("resolving root path: %w", err)
	}

	provider, err := providers.NewGeminiProvider(cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.EmbeddingModel)
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}

	db := store.NewQdrantStore(cfg.Storage.URL, cfg.Storage.CollectionPrefix, cfg.Project.Name)

	srv := NewServer(db, provider, rootPath)
	return srv.ServeStdio()
}

func (s *Server) handleGetSummary(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return mcp.NewToolResultError("file_path parameter is required"), nil
	}

	point, err := s.store.GetPointByFilePath(filePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get file summary: %v", err)), nil
	}
	if point == nil {
		return mcp.NewToolResultError(fmt.Sprintf("file '%s' is not indexed", filePath)), nil
	}

	resp := map[string]any{
		"file_path":        point.FilePath,
		"summary":          point.Summary,
		"responsibilities": point.Responsibilities,
		"domain":           point.Domain,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal response: %v", err)), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}
