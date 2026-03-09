package main

import (
	"fmt"
	"log"
	"os"

	"VedCode/internal/indexer"
	"VedCode/internal/mcp"
)

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	configPath := ".vedcode.yml"
	if len(os.Args) > 2 {
		configPath = os.Args[2]
	}

	var err error
	switch command {
	case "indexer":
		err = indexer.Run(configPath)
	case "mcp":
		err = mcp.RunServer(configPath)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: vedcode <command> [config-path]\n\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  indexer    Index project files for semantic search\n")
	fmt.Fprintf(os.Stderr, "  mcp        Start MCP server (STDIO transport)\n")
	fmt.Fprintf(os.Stderr, "\nConfig path defaults to .vedcode.yml\n")
}
