package prompts

import (
	"strings"
	"testing"
)

func TestRender_ProjectStructureAnalysis(t *testing.T) {
	tree := "├── cmd/\n│   └── main.go\n└── internal/\n    └── app.go"

	result, err := Render("ProjectStructureAnalysis.md", map[string]string{
		"CONTENT": tree,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	result, err := Render("SourceCodeAnalysis.md", map[string]string{
		"CONTENT":          code,
		"PROJECT_OVERVIEW": overview,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

func TestRender_UnknownTemplate(t *testing.T) {
	_, err := Render("NonExistent.md", nil)
	if err == nil {
		t.Fatal("expected error for unknown template, got nil")
	}
}

func TestRender_NilVars(t *testing.T) {
	result, err := Render("ProjectStructureAnalysis.md", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "${CONTENT}") {
		t.Errorf("expected ${CONTENT} placeholder to remain when no vars provided")
	}
}

func TestRender_EmptyVars(t *testing.T) {
	result, err := Render("ProjectStructureAnalysis.md", map[string]string{
		"CONTENT": "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, "${CONTENT}") {
		t.Errorf("rendered template still contains ${CONTENT} placeholder")
	}
}
