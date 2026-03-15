package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"VedCode/internal/config"
	"VedCode/internal/providers"
	"VedCode/internal/store"
)

const serverInstructions = `VedCode is a semantic code navigation server. It indexes project files and provides AI-friendly semantic search over the codebase.

WHEN TO USE:
- Use get_project_overview FIRST when you need to understand a new or unfamiliar project
- Use search_code to find files by intent or concept (e.g. "authentication logic", "database migrations", "error handling middleware") — this is semantic search, not keyword search
- Use get_summary to get detailed information about a specific file you already know the path of

RECOMMENDED WORKFLOW:
1. Start with get_project_overview to understand the project architecture
2. Use search_code with natural language queries to find relevant files
3. Use get_summary on specific files to get their responsibilities and domain

TIPS:
- search_code works best with descriptive queries about what the code does, not exact filenames
- If search returns no useful results, try rephrasing the query with different terms
- get_project_overview is cheap and fast — use it liberally when orienting in a codebase`

// Server wraps the MCP server with VedCode-specific tool handlers.
type Server struct {
	mcpServer *server.MCPServer
	store     store.Store
	provider  providers.EmbeddingProvider
	rootPath  string
	logger    *slog.Logger
}

// NewServer creates a new MCP server with all VedCode tools registered.
func NewServer(st store.Store, provider providers.EmbeddingProvider, rootPath string, logger *slog.Logger) *Server {
	s := &Server{
		store:    st,
		provider: provider,
		rootPath: rootPath,
		logger:   logger,
	}

	s.mcpServer = server.NewMCPServer(
		"VedCode",
		"1.0.0",
		server.WithToolCapabilities(false),
		server.WithInstructions(serverInstructions),
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

	s.logger.Debug("handleSearchCode request",
		"query", query,
		"limit", limit,
	)
	start := time.Now()

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

	s.logger.Debug("handleSearchCode response",
		"query", query,
		"results_count", len(results),
		"response", string(data),
		"duration", time.Since(start),
	)

	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleGetProjectOverview(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	overviewPath := filepath.Join(s.rootPath, ".vedcode", "project_overview.md")

	s.logger.Debug("handleGetProjectOverview request", "path", overviewPath)

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

	s.logger.Debug("handleGetProjectOverview response",
		"response", string(data),
	)

	return mcp.NewToolResultText(string(data)), nil
}

// RunServer loads config, initializes dependencies, and starts the MCP server.
func RunServer(configPath string, logger *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger.Debug("config loaded",
		"project", cfg.Project.Name,
		"embedding_provider", cfg.Embedding.Provider,
		"embedding_model", cfg.Embedding.Model,
		"storage_url", cfg.Storage.URL,
	)

	rootPath, err := filepath.Abs(cfg.Project.RootPath)
	if err != nil {
		return fmt.Errorf("resolving root path: %w", err)
	}

	embedder, err := providers.NewEmbeddingProvider(cfg.Embedding, logger)
	if err != nil {
		return fmt.Errorf("creating embedding provider: %w", err)
	}

	// Determine vector size: use config value or auto-detect from provider
	vectorSize := cfg.Embedding.VectorSize
	if vectorSize <= 0 {
		vectorSize, err = embedder.DetectVectorSize()
		if err != nil {
			return fmt.Errorf("detecting vector size: %w", err)
		}
		logger.Info("Auto-detected vector size", "vector_size", vectorSize)
	}

	db := store.NewQdrantStore(cfg.Storage.URL, cfg.Storage.CollectionPrefix, cfg.Project.Name, vectorSize, logger)

	srv := NewServer(db, embedder, rootPath, logger)

	logger.Debug("MCP server starting", "root_path", rootPath)
	return srv.ServeStdio()
}

func (s *Server) handleGetSummary(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := request.RequireString("file_path")
	if err != nil {
		return mcp.NewToolResultError("file_path parameter is required"), nil
	}

	s.logger.Debug("handleGetSummary request", "file_path", filePath)

	point, err := s.store.GetPointByFilePath(filePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get file summary: %v", err)), nil
	}
	if point == nil {
		s.logger.Debug("handleGetSummary: not indexed", "file_path", filePath)
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

	s.logger.Debug("handleGetSummary response",
		"file_path", filePath,
		"response", string(data),
	)

	return mcp.NewToolResultText(string(data)), nil
}
