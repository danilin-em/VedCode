package prompts

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed ProjectStructureAnalysis.md SourceCodeAnalysis.md
var templates embed.FS

// Render reads the named template from embedded files and replaces
// all occurrences of variables in the form ${KEY} with the provided values.
// Unknown variables are left as-is.
func Render(templateName string, vars map[string]string) (string, error) {
	data, err := templates.ReadFile(templateName)
	if err != nil {
		return "", fmt.Errorf("read template %q: %w", templateName, err)
	}

	result := string(data)
	for key, value := range vars {
		result = strings.ReplaceAll(result, "${"+key+"}", value)
	}

	return result, nil
}
