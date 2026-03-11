package prompts

import "strings"

// DefaultProjectStructureAnalysis is the built-in prompt for project structure analysis.
const DefaultProjectStructureAnalysis = `You are a senior software architect. Analyze the following project file tree and produce a concise overview of the project.

## File Tree

${CONTENT}

## Instructions

Based on the file tree above, determine and describe:

1. **Framework / Platform** — What programming language(s), framework(s), or platform is this project built on? (e.g., Laravel 11, Next.js 14, Spring Boot 3, Go with Chi)
2. **Architecture** — What architectural style does the project follow? (e.g., MVC, Clean Architecture, Hexagonal, Modular Monolith, Microservices)
3. **Modules / Components** — List the main modules, packages, or top-level components and briefly describe their purpose.
4. **Domains** — Identify the business domains or bounded contexts present in the project (e.g., Payments, Users, Orders, Notifications).
5. **Patterns** — Note any recognizable design patterns or conventions (e.g., Repository pattern, CQRS, Event-driven, Dependency Injection, middleware pipeline).

## Output Format

Write a structured overview in Markdown. Keep it concise — no more than 300 words. Focus on facts observable from the file tree. Do not speculate about implementation details you cannot infer from file names and directory structure alone.`

// DefaultSourceCodeAnalysis is the built-in prompt for source code file analysis.
const DefaultSourceCodeAnalysis = "You are a senior software engineer. Analyze the following source code file and produce a structured semantic description that will be used for search and navigation.\n\n## Project Context\n\n${PROJECT_OVERVIEW}\n\n## Source Code\n\n```\n${CONTENT}\n```\n\n## Instructions\n\nAnalyze the source code above and provide:\n\n1. **Summary** — A concise 1–3 sentence description of what this file does and why it exists in the project. Write it so that a developer searching for this functionality would find it through a semantic search query.\n2. **Responsibilities** — A list of specific responsibilities this file handles (e.g., \"Validates user input\", \"Sends email notifications\", \"Manages database connections\"). List 2–6 items.\n3. **Domain** — The business domain or bounded context this file belongs to (e.g., Payments, Auth, Infrastructure, Configuration). Use a single word or short phrase.\n4. **Language** — The programming language of the file.\n\nRespond in JSON format."

// DefaultDirectoryAnalysis is the built-in prompt for directory analysis.
const DefaultDirectoryAnalysis = `You are a senior software architect. Analyze the following directory and produce a concise semantic description for code navigation.

## Directory: ${DIR_PATH}

## Project Context

${PROJECT_OVERVIEW}

## Direct Files

${FILES_SUMMARIES}

## Subdirectories

${SUBDIRS_SUMMARIES}

## Instructions

Based on the files and subdirectories above, provide:

1. **Summary** — A concise 1–3 sentence description of what this directory contains and its role in the project. Write it so that a developer searching for this functionality would find it through a semantic search query.
2. **Responsibilities** — A list of 2–6 specific responsibilities this directory handles.
3. **Domain** — The business domain or bounded context (single word or short phrase).

Respond in JSON format.`

// Render replaces all occurrences of ${KEY} variables in the template
// with the provided values. Unknown variables are left as-is.
func Render(template string, vars map[string]string) string {
	result := template
	for key, value := range vars {
		result = strings.ReplaceAll(result, "${"+key+"}", value)
	}
	return result
}
