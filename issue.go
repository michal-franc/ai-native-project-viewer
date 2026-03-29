package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"gopkg.in/yaml.v3"
)

type Issue struct {
	Title    string   `yaml:"title"`
	Status   string   `yaml:"status"`
	System   string   `yaml:"system"`
	Version  string   `yaml:"version"`
	Labels   []string `yaml:"labels"`
	Priority string   `yaml:"priority"`
	Assignee string   `yaml:"assignee"`
	Created  string   `yaml:"created"`

	// Computed fields
	Slug     string    `yaml:"-"`
	FilePath string    `yaml:"-"`
	ModTime  time.Time `yaml:"-"`
	BodyHTML string    `yaml:"-"`
	BodyRaw  string    `yaml:"-"`
}

func ParseIssue(filename string, data []byte) (*Issue, error) {
	issue := &Issue{}
	rawBody, err := ParseFrontmatter(string(data), issue)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}
	body, _ := ParseComments(rawBody)
	body = strings.TrimSpace(body)
	issue.BodyRaw = body

	md := goldmark.New(goldmark.WithExtensions(extension.TaskList, extension.Table))
	var buf bytes.Buffer
	if err := md.Convert([]byte(body), &buf); err != nil {
		return nil, fmt.Errorf("rendering markdown in %s: %w", filename, err)
	}
	issue.BodyHTML = buf.String()

	// Slug from title (slugified)
	issue.Slug = slugify(issue.Title)

	// Normalize
	issue.Status = strings.ToLower(strings.TrimSpace(issue.Status))
	issue.Priority = strings.ToLower(strings.TrimSpace(issue.Priority))
	issue.System = strings.TrimSpace(issue.System)

	return issue, nil
}

func LoadIssues(dir string) ([]*Issue, error) {
	var issues []*Issue

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", path, err)
			return nil
		}

		issue, err := ParseIssue(d.Name(), data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", path, err)
			return nil
		}

		issue.FilePath = path

		// File modification time for sorting
		if info, err := d.Info(); err == nil {
			issue.ModTime = info.ModTime()
		}

		// Prefix slug with system subdirectory for uniqueness
		relDir := filepath.Dir(path)
		baseDir, _ := filepath.Rel(dir, relDir)
		if baseDir != "" && baseDir != "." {
			issue.Slug = strings.ToLower(baseDir) + "/" + issue.Slug
		}

		issues = append(issues, issue)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking directory %s: %w", dir, err)
	}

	// Handle slug collisions by appending -2, -3, etc.
	seen := map[string]int{}
	for _, issue := range issues {
		count := seen[issue.Slug]
		seen[issue.Slug]++
		if count > 0 {
			issue.Slug = fmt.Sprintf("%s-%d", issue.Slug, count+1)
		}
	}

	// Default sort: most recently modified first
	sort.Slice(issues, func(i, j int) bool {
		return issues[i].ModTime.After(issues[j].ModTime)
	})

	return issues, nil
}

type IssueUpdate struct {
	Status   *string  `json:"status,omitempty"`
	Priority *string  `json:"priority,omitempty"`
	Version  *string  `json:"version,omitempty"`
	Assignee *string  `json:"assignee,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	Body     *string  `json:"body,omitempty"`
}

func UpdateIssueFrontmatter(filePath string, update IssueUpdate) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filePath, err)
	}

	content := string(data)
	if !strings.HasPrefix(content, "---") {
		return fmt.Errorf("no frontmatter in %s", filePath)
	}

	parts := strings.SplitN(content[3:], "---", 2)
	if len(parts) < 2 {
		return fmt.Errorf("invalid frontmatter in %s", filePath)
	}

	// Parse existing frontmatter as ordered map to preserve structure
	var fm yaml.Node
	if err := yaml.Unmarshal([]byte(parts[0]), &fm); err != nil {
		return fmt.Errorf("parsing frontmatter: %w", err)
	}

	// Simpler approach: parse into map, update, re-serialize
	existing := map[string]interface{}{}
	if err := yaml.Unmarshal([]byte(parts[0]), &existing); err != nil {
		return fmt.Errorf("parsing frontmatter: %w", err)
	}

	if update.Status != nil {
		existing["status"] = *update.Status
	}
	if update.Priority != nil {
		if *update.Priority == "" {
			delete(existing, "priority")
		} else {
			existing["priority"] = *update.Priority
		}
	}
	if update.Version != nil {
		if *update.Version == "" {
			delete(existing, "version")
		} else {
			existing["version"] = *update.Version
		}
	}
	if update.Assignee != nil {
		if *update.Assignee == "" {
			delete(existing, "assignee")
		} else {
			existing["assignee"] = *update.Assignee
		}
	}
	if update.Labels != nil {
		if len(update.Labels) == 0 {
			delete(existing, "labels")
		} else {
			existing["labels"] = update.Labels
		}
	}

	newFM, err := yaml.Marshal(existing)
	if err != nil {
		return fmt.Errorf("serializing frontmatter: %w", err)
	}

	body := parts[1]
	if update.Body != nil {
		body = "\n" + *update.Body + "\n"
	}

	var out strings.Builder
	out.WriteString("---\n")
	out.Write(newFM)
	out.WriteString("---")
	out.WriteString(body)

	return os.WriteFile(filePath, []byte(out.String()), 0644)
}

func CollectFilterValues(issues []*Issue) (statuses, systems, priorities, labels, assignees []string) {
	statusSet := map[string]bool{}
	systemSet := map[string]bool{}
	prioritySet := map[string]bool{}
	labelSet := map[string]bool{}
	assigneeSet := map[string]bool{}

	for _, issue := range issues {
		if issue.Status != "" {
			statusSet[issue.Status] = true
		}
		if issue.System != "" {
			systemSet[issue.System] = true
		}
		if issue.Priority != "" {
			prioritySet[issue.Priority] = true
		}
		if issue.Assignee != "" {
			assigneeSet[issue.Assignee] = true
		}
		for _, l := range issue.Labels {
			labelSet[l] = true
		}
	}

	for k := range statusSet {
		statuses = append(statuses, k)
	}
	for k := range systemSet {
		systems = append(systems, k)
	}
	for k := range prioritySet {
		priorities = append(priorities, k)
	}
	for k := range labelSet {
		labels = append(labels, k)
	}
	for k := range assigneeSet {
		assignees = append(assignees, k)
	}

	sort.Strings(statuses)
	sort.Strings(systems)
	sort.Strings(priorities)
	sort.Strings(labels)
	sort.Strings(assignees)

	return
}
