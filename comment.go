package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const commentMarkerStart = "<!-- issue-viewer-comments"
const commentMarkerEnd = "-->"

type Comment struct {
	ID     int    `json:"id"`
	Block  int    `json:"block"`
	Date   string `json:"date"`
	Text   string `json:"text"`
	Status string `json:"status"` // "open" or "done"
	Source string `json:"source"` // "app" (human via UI) or "" (bot/script)
}

// ParseComments extracts comments from the bottom of a markdown file.
func ParseComments(content string) (body string, comments []Comment) {
	idx := strings.LastIndex(content, commentMarkerStart)
	if idx == -1 {
		return content, nil
	}

	body = strings.TrimRight(content[:idx], "\n")

	block := content[idx+len(commentMarkerStart):]
	endIdx := strings.Index(block, commentMarkerEnd)
	if endIdx == -1 {
		return body, nil
	}

	block = strings.TrimSpace(block[:endIdx])
	scanner := bufio.NewScanner(strings.NewReader(block))
	nextID := 1
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var c Comment
		if err := json.Unmarshal([]byte(line), &c); err == nil {
			if c.Status == "" {
				c.Status = "open"
			}
			if c.ID == 0 {
				c.ID = nextID
			}
			if c.ID >= nextID {
				nextID = c.ID + 1
			}
			comments = append(comments, c)
		}
	}

	return body, comments
}

// SerializeComments renders comments as an HTML comment block.
func SerializeComments(comments []Comment) string {
	if len(comments) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(commentMarkerStart)
	b.WriteString("\n")
	for _, c := range comments {
		data, _ := json.Marshal(c)
		b.Write(data)
		b.WriteString("\n")
	}
	b.WriteString(commentMarkerEnd)
	return b.String()
}

func saveComments(filePath string, comments []Comment) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filePath, err)
	}

	body, _ := ParseComments(string(data))
	result := body + SerializeComments(comments)
	return os.WriteFile(filePath, []byte(result), 0644)
}

func nextCommentID(comments []Comment) int {
	max := 0
	for _, c := range comments {
		if c.ID > max {
			max = c.ID
		}
	}
	return max + 1
}

// AddComment appends a comment to the issue file.
func AddComment(filePath string, block int, text, source string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filePath, err)
	}

	_, existing := ParseComments(string(data))

	c := Comment{
		ID:     nextCommentID(existing),
		Block:  block,
		Date:   time.Now().Format("2006-01-02"),
		Text:   text,
		Status: "open",
		Source: source,
	}
	existing = append(existing, c)

	return saveComments(filePath, existing)
}

// ToggleComment toggles a comment between open and done.
func ToggleComment(filePath string, commentID int) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filePath, err)
	}

	_, comments := ParseComments(string(data))
	for i := range comments {
		if comments[i].ID == commentID {
			if comments[i].Status == "done" {
				comments[i].Status = "open"
			} else {
				comments[i].Status = "done"
			}
			break
		}
	}

	return saveComments(filePath, comments)
}

// DeleteComment removes a comment by ID.
func DeleteComment(filePath string, commentID int) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filePath, err)
	}

	_, comments := ParseComments(string(data))
	var filtered []Comment
	for _, c := range comments {
		if c.ID != commentID {
			filtered = append(filtered, c)
		}
	}

	return saveComments(filePath, filtered)
}

// LoadComments reads comments from an issue file.
func LoadComments(filePath string) ([]Comment, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}

	_, comments := ParseComments(string(data))
	return comments, nil
}
