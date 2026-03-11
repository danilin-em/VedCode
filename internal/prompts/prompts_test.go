package prompts

import (
	"strings"
	"testing"
)

func TestRender_ProjectStructureAnalysis(t *testing.T) {
	tree := "├── cmd/\n│   └── main.go\n└── internal/\n    └── app.go"

	result := Render(DefaultProjectStructureAnalysis, map[string]string{
		"CONTENT": tree,
	})

	if !strings.Contains(result, tree) {
		t.Errorf("rendered template does not contain the file tree")
	}
	if strings.Contains(result, "${CONTENT}") {
		t.Errorf("rendered template still contains ${CONTENT} placeholder")
	}
}

func TestRender_SourceCodeAnalysis(t *testing.T) {
	code := "package main\n\nfunc main() {}\n"
	overview := "Go CLI application with modular architecture."

	result := Render(DefaultSourceCodeAnalysis, map[string]string{
		"CONTENT":          code,
		"PROJECT_OVERVIEW": overview,
	})

	if !strings.Contains(result, code) {
		t.Errorf("rendered template does not contain the source code")
	}
	if !strings.Contains(result, overview) {
		t.Errorf("rendered template does not contain the project overview")
	}
	if strings.Contains(result, "${CONTENT}") {
		t.Errorf("rendered template still contains ${CONTENT} placeholder")
	}
	if strings.Contains(result, "${PROJECT_OVERVIEW}") {
		t.Errorf("rendered template still contains ${PROJECT_OVERVIEW} placeholder")
	}
}

func TestRender_CustomTemplate(t *testing.T) {
	result := Render("Custom prompt: ${CONTENT}", map[string]string{
		"CONTENT": "test-data",
	})
	if result != "Custom prompt: test-data" {
		t.Errorf("result = %q, want %q", result, "Custom prompt: test-data")
	}
}

func TestRender_NilVars(t *testing.T) {
	result := Render(DefaultProjectStructureAnalysis, nil)

	if !strings.Contains(result, "${CONTENT}") {
		t.Errorf("expected ${CONTENT} placeholder to remain when no vars provided")
	}
}

func TestRender_EmptyVars(t *testing.T) {
	result := Render(DefaultProjectStructureAnalysis, map[string]string{
		"CONTENT": "",
	})

	if strings.Contains(result, "${CONTENT}") {
		t.Errorf("rendered template still contains ${CONTENT} placeholder")
	}
}
