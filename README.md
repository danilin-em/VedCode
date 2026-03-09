# VedCode

VedCode is an intelligent semantic search and code analysis tool. It processes your codebase using Large Language Models (LLMs) to understand project architecture and file responsibilities, stores these representations as embeddings in a vector database, and exposes this knowledge through a Model Context Protocol (MCP) server.

This allows AI coding assistants (like Claude Desktop/Code, Codex, Gemini, Cursor, and others) to perform deep, semantic queries across your entire project, retrieve architectural overviews, and understand the specific context of any file.

## Features

- **Smart Codebase Indexing:** Traverses your project while respecting `.gitignore` and custom exclusion patterns.
- **LLM-Powered Analysis:** Uses Google's Gemini models to generate high-level architectural overviews and detailed semantic summaries (responsibilities, domain, language) for every file.
- **Vector Search with Qdrant:** Embeds file summaries and stores them in Qdrant for blazing-fast, semantic similarity searches.
- **Incremental Indexing:** Built-in file hashing ensures only modified files are re-analyzed on subsequent index runs, saving time and API costs.
- **MCP Server Integration:** Exposes an MCP server over STDIO with built-in tools:
  - `search_code`: Perform semantic search for project files by natural language description.
  - `get_project_overview`: Retrieve the project's high-level architectural overview.
  - `get_summary`: Get the detailed semantic summary of a specific file.

## Prerequisites

- **Go 1.25+**
- **Qdrant**: A running instance of Qdrant (e.g., via Docker: `docker run -p 6333:6333 qdrant/qdrant`).
- **Gemini API Key**: You will need an API key from Google AI Studio.

## Installation

Clone the repository and build the binary using the provided `Makefile`:

```bash
git clone https://github.com/yourusername/VedCode.git
cd VedCode
make build
```

The compiled binary will be available at `dist/vedcode`.

## Configuration

VedCode uses a `.vedcode.yml` file located at the root of the project you want to index.

Example `.vedcode.yml`:

```yaml
project:
  name: "my-awesome-project"
  root_path: "."

indexer:
  max_file_size: 1048576  # 1 MB
  ignore_patterns:
    - "*.min.js"
    - "*.map"
    - "dist/*"

llm:
  provider: "gemini"
  api_key: "YOUR_GEMINI_API_KEY"
  model: "gemini-2.5-flash"
  embedding_model: "gemini-embedding-001"

storage:
  type: "qdrant"
  url: "http://localhost:6333"
  collection_prefix: "vedcode_"
```

## Usage

VedCode provides two main commands:

### 1. Indexing the Project

Before searching, you must index your codebase. Run the indexer from the root of your project:

```bash
vedcode indexer [.vedcode.yml]
```
This will analyze the project structure, generate an overview (saved to `.vedcode/project_overview.md`), analyze each file, and store the embeddings in Qdrant.

### 2. Running the MCP Server

Once the project is indexed, you can start the MCP server. It communicates over standard input/output (STDIO), which is the standard mechanism for MCP clients.

```bash
vedcode mcp [.vedcode.yml]
```

To use VedCode with an MCP-compatible client (like Claude Desktop), add it to your client's MCP configuration:

```json
{
  "mcpServers": {
    "vedcode": {
      "command": "/path/to/dist/vedcode",
      "args": ["mcp", "/path/to/project/.vedcode.yml"]
    }
  }
}
```

## Available MCP Tools

When connected to an MCP client, VedCode exposes the following tools to the AI:

- **`search_code`**: Returns files matching a natural language query with relevance scores.
  - Parameters: `query` (string, required), `limit` (number, optional, default: 5)
- **`get_project_overview`**: Returns the high-level project architecture overview.
- **`get_summary`**: Returns the semantic summary, responsibilities, and domain of a specific file.
  - Parameters: `file_path` (string, required)

## Development

- `make test` - Run tests
- `make lint` - Run golangci-lint
- `make fmt` - Format code
- `make tidy` - Clean up go.mod dependencies
