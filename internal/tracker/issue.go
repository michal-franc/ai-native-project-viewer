package tracker

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

// StatusOrder defines the workflow lifecycle.
var StatusOrder = []string{
	"none", "idea", "in design", "backlog", "in progress", "testing", "documentation", "done",
}

var StatusDescriptions = map[string]string{
	"none":          "",
	"idea":          "Raw idea, needs exploration",
	"in design":     "Being designed and specced out",
	"backlog":       "Ready to work on",
	"in progress":   "Actively being implemented",
	"testing":       "Under verification",
	"documentation": "Being documented",
	"done":          "Completed",
}

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

	issue.Slug = Slugify(issue.Title)

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

		if info, err := d.Info(); err == nil {
			issue.ModTime = info.ModTime()
		}

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

	// Handle slug collisions
	seen := map[string]int{}
	for _, issue := range issues {
		count := seen[issue.Slug]
		seen[issue.Slug]++
		if count > 0 {
			issue.Slug = fmt.Sprintf("%s-%d", issue.Slug, count+1)
		}
	}

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
		// Preserve existing comments when updating body
		_, existingComments := ParseComments(body)
		body = "\n" + *update.Body + "\n" + SerializeComments(existingComments)
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

// StatusIndex returns the position of a status in the workflow, or -1.
func StatusIndex(status string) int {
	for i, s := range StatusOrder {
		if s == status {
			return i
		}
	}
	return -1
}

// ValidTransition checks if moving from one status to the next is allowed.
func ValidTransition(from, to string) bool {
	fi := StatusIndex(from)
	ti := StatusIndex(to)
	if fi == -1 || ti == -1 {
		return false
	}
	return ti == fi+1
}

// CountCheckboxes returns total and checked checkbox counts in markdown.
func CountCheckboxes(body string) (total, checked int) {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "- [X]") {
			total++
			checked++
		} else if strings.HasPrefix(trimmed, "- [ ]") {
			total++
		}
	}
	return
}

// HasTestPlan checks if the body has ## Test Plan with ### Automated and ### Manual.
func HasTestPlan(body string) (hasAutomated, hasManual bool) {
	inTestPlan := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "## Test Plan") {
			inTestPlan = true
			continue
		}
		if inTestPlan && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if inTestPlan {
			if strings.EqualFold(trimmed, "### Automated") {
				hasAutomated = true
			}
			if strings.EqualFold(trimmed, "### Manual") {
				hasManual = true
			}
		}
	}
	return
}

// HasCommentWithPrefix checks if any comment starts with the given prefix.
func HasCommentWithPrefix(comments []Comment, prefix string) bool {
	for _, c := range comments {
		if strings.HasPrefix(strings.ToLower(c.Text), strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}
