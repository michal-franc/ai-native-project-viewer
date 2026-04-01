package tracker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// StatusOrder defines the workflow lifecycle.
var StatusOrder = []string{
	"idea", "in design", "backlog", "in progress", "testing", "human-testing", "documentation", "done",
}

var StatusDescriptions = map[string]string{
	"idea":          "Raw idea, needs exploration",
	"in design":     "Being designed and specced out",
	"backlog":       "Ready to work on",
	"in progress":   "Actively being implemented",
	"testing":       "Under verification",
	"human-testing": "Manual verification by humans",
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

	fmRaw := parts[0]
	body := parts[1]

	// Line-level frontmatter editing to preserve ordering, types, and unmodified fields
	setScalar := func(fm, key, value string) string {
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:.*$`)
		newLine := key + `: "` + value + `"`
		if re.MatchString(fm) {
			return re.ReplaceAllString(fm, newLine)
		}
		// Append before the end
		return strings.TrimRight(fm, "\n") + "\n" + newLine + "\n"
	}

	removeField := func(fm, key string) string {
		// Remove scalar field
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:.*\n?`)
		fm = re.ReplaceAllString(fm, "")
		// Remove list field (key: followed by indented - lines)
		reList := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:\s*\n([ \t]+-[^\n]*\n?)*`)
		fm = reList.ReplaceAllString(fm, "")
		return fm
	}

	setLabels := func(fm string, labels []string) string {
		fm = removeField(fm, "labels")
		if len(labels) == 0 {
			return fm
		}
		var buf strings.Builder
		buf.WriteString("labels:\n")
		for _, l := range labels {
			buf.WriteString("  - " + l + "\n")
		}
		return strings.TrimRight(fm, "\n") + "\n" + buf.String()
	}

	if update.Status != nil {
		fmRaw = setScalar(fmRaw, "status", *update.Status)
	}
	if update.Priority != nil {
		if *update.Priority == "" {
			fmRaw = removeField(fmRaw, "priority")
		} else {
			fmRaw = setScalar(fmRaw, "priority", *update.Priority)
		}
	}
	if update.Version != nil {
		if *update.Version == "" {
			fmRaw = removeField(fmRaw, "version")
		} else {
			fmRaw = setScalar(fmRaw, "version", *update.Version)
		}
	}
	if update.Assignee != nil {
		if *update.Assignee == "" {
			fmRaw = removeField(fmRaw, "assignee")
		} else {
			fmRaw = setScalar(fmRaw, "assignee", *update.Assignee)
		}
	}
	if update.Labels != nil {
		fmRaw = setLabels(fmRaw, update.Labels)
	}

	if update.Body != nil {
		_, existingComments := ParseComments(body)
		body = "\n" + *update.Body + "\n" + SerializeComments(existingComments)
	}

	var out strings.Builder
	out.WriteString("---")
	out.WriteString(fmRaw)
	out.WriteString("---")
	out.WriteString(body)

	return os.WriteFile(filePath, []byte(out.String()), 0644)
}

// DeleteIssue removes the issue markdown file and its comment sidecar if present.
func DeleteIssue(filePath string) error {
	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("deleting %s: %w", filePath, err)
	}
	// Also remove comment sidecar if it exists
	commentFile := strings.TrimSuffix(filePath, ".md") + ".comments.yaml"
	os.Remove(commentFile) // ignore error — may not exist
	return nil
}

// CreateIssueFile creates a new issue markdown file and returns the file path and slug.
type CreateIssueOpts struct {
	Title    string
	Status   string
	System   string
	Version  string
	Priority string
	Labels   []string
	Body     string
}

func CreateIssueFile(issueDir, title, status, system, version string) (filePath, slug string, err error) {
	return CreateIssueFileOpts(issueDir, CreateIssueOpts{
		Title: title, Status: status, System: system, Version: version,
	})
}

func CreateIssueFileOpts(issueDir string, opts CreateIssueOpts) (filePath, slug string, err error) {
	if opts.Title == "" {
		return "", "", fmt.Errorf("title is required")
	}
	if opts.Status == "" {
		opts.Status = "idea"
	}

	dir := issueDir
	if opts.System != "" {
		dir = filepath.Join(dir, opts.System)
		os.MkdirAll(dir, 0755)
	}

	slug = Slugify(opts.Title)
	filename := filepath.Join(dir, slug+".md")

	var content strings.Builder
	content.WriteString("---\n")
	content.WriteString(fmt.Sprintf("title: \"%s\"\n", strings.ReplaceAll(opts.Title, "\"", "\\\"")))
	content.WriteString(fmt.Sprintf("status: \"%s\"\n", opts.Status))
	if opts.System != "" {
		content.WriteString(fmt.Sprintf("system: \"%s\"\n", opts.System))
	}
	if opts.Version != "" {
		content.WriteString(fmt.Sprintf("version: \"%s\"\n", opts.Version))
	}
	if opts.Priority != "" {
		content.WriteString(fmt.Sprintf("priority: \"%s\"\n", opts.Priority))
	}
	if len(opts.Labels) > 0 {
		content.WriteString("labels:\n")
		for _, l := range opts.Labels {
			content.WriteString(fmt.Sprintf("  - %s\n", l))
		}
	}
	content.WriteString("---\n")
	if opts.Body != "" {
		content.WriteString("\n" + opts.Body + "\n")
	} else {
		content.WriteString("\n")
	}

	if err := os.WriteFile(filename, []byte(content.String()), 0644); err != nil {
		return "", "", fmt.Errorf("creating issue: %w", err)
	}

	if opts.System != "" {
		slug = strings.ToLower(opts.System) + "/" + slug
	}

	return filename, slug, nil
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

// CountCheckboxesInSection counts checkboxes only under a specific ## heading.
// The section ends at the next ## heading or end of body.
func CountCheckboxesInSection(body, section string) (total, checked int) {
	inSection := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			heading := strings.TrimSpace(strings.TrimPrefix(trimmed, "##"))
			inSection = strings.EqualFold(heading, section)
			continue
		}
		if !inSection {
			continue
		}
		if strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "- [X]") {
			total++
			checked++
		} else if strings.HasPrefix(trimmed, "- [ ]") {
			total++
		}
	}
	return
}

// CheckCheckbox finds an unchecked checkbox whose text contains the query and checks it off.
// Returns the updated body and whether a match was found.
func CheckCheckbox(body, query string) (string, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") {
			text := strings.ToLower(strings.TrimSpace(trimmed[5:]))
			if strings.Contains(text, query) {
				lines[i] = strings.Replace(line, "- [ ]", "- [x]", 1)
				return strings.Join(lines, "\n"), true
			}
		}
	}
	return body, false
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
