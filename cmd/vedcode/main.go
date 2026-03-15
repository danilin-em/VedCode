package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"VedCode/internal/indexer"
	"VedCode/internal/mcp"
	"VedCode/internal/trace"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	configPath := ".vedcode.yml"
	force := false
	traceEnabled := false
	traceLogPath := ""

	// Parse remaining arguments: positional config-path and flags
	for _, arg := range os.Args[2:] {
		switch {
		case arg == "--force":
			force = true
		case arg == "--trace":
			traceEnabled = true
		case strings.HasPrefix(arg, "--trace-log="):
			traceLogPath = strings.TrimPrefix(arg, "--trace-log=")
			traceEnabled = true
		default:
			configPath = arg
		}
	}

	// For MCP server with --trace but no explicit path, use auto file
	// (MCP uses stdout/stderr for protocol, so trace must go to a file)
	if command == "mcp" && traceEnabled && traceLogPath == "" {
		traceLogPath = filepath.Join(".vedcode", "mcp-trace.log")
	}

	console := command == "indexer"
	logger, closer, err := trace.NewLogger(traceEnabled, traceLogPath, console)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if closer != nil {
		defer closer.Close()
	}

	switch command {
	case "indexer":
		err = indexer.Run(configPath, force, logger)
	case "mcp":
		err = mcp.RunServer(configPath, logger)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: vedcode <command> [config-path] [flags]\n\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  indexer    Index project files for semantic search\n")
	fmt.Fprintf(os.Stderr, "  mcp        Start MCP server (STDIO transport)\n")
	fmt.Fprintf(os.Stderr, "\nFlags:\n")
	fmt.Fprintf(os.Stderr, "  --force              Delete existing index and re-index from scratch (indexer only)\n")
	fmt.Fprintf(os.Stderr, "  --trace              Enable trace logging (stderr for indexer, .vedcode/mcp-trace.log for mcp)\n")
	fmt.Fprintf(os.Stderr, "  --trace-log=<path>   Enable trace logging to a specific file\n")
	fmt.Fprintf(os.Stderr, "\nConfig path defaults to .vedcode.yml\n")
}
