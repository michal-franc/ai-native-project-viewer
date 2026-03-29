package main

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseFrontmatter splits a markdown file into YAML frontmatter and body.
// Returns the parsed frontmatter destination and the remaining body text.
func ParseFrontmatter(content string, dest interface{}) (body string, err error) {
	if !strings.HasPrefix(content, "---") {
		return "", fmt.Errorf("missing frontmatter delimiter")
	}

	parts := strings.SplitN(content[3:], "---", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("missing closing frontmatter delimiter")
	}

	if err := yaml.Unmarshal([]byte(parts[0]), dest); err != nil {
		return "", fmt.Errorf("parsing frontmatter YAML: %w", err)
	}

	return strings.TrimSpace(parts[1]), nil
}
