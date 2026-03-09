You are a senior software engineer. Analyze the following source code file and produce a structured semantic description that will be used for search and navigation.

## Project Context

${PROJECT_OVERVIEW}

## Source Code

${CONTENT}

## Instructions

Analyze the source code above and provide:

1. **Summary** — A concise 1–3 sentence description of what this file does and why it exists in the project. Write it so that a developer searching for this functionality would find it through a semantic search query.
2. **Responsibilities** — A list of specific responsibilities this file handles (e.g., "Validates user input", "Sends email notifications", "Manages database connections"). List 2–6 items.
3. **Domain** — The business domain or bounded context this file belongs to (e.g., Payments, Auth, Infrastructure, Configuration). Use a single word or short phrase.
4. **Language** — The programming language of the file.

## Output Format

Respond strictly in the following JSON format with no additional text:

```json
{
  "summary": "...",
  "responsibilities": ["...", "..."],
  "domain": "...",
  "language": "..."
}
```
