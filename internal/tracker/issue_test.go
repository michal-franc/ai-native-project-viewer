package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIssue_Valid(t *testing.T) {
	data := []byte(`---
title: "My Issue"
status: "in progress"
system: "Combat"
version: "0.1"
labels:
  - bug
  - enhancement
priority: "high"
assignee: "alice"
created: "2025-01-15"
---

This is the body.
`)

	issue, err := ParseIssue("test.md", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if issue.Title != "My Issue" {
		t.Errorf("title = %q, want %q", issue.Title, "My Issue")
	}
	if issue.Status != "in progress" {
		t.Errorf("status = %q, want %q", issue.Status, "in progress")
	}
	if issue.System != "Combat" {
		t.Errorf("system = %q, want %q", issue.System, "Combat")
	}
	if issue.Version != "0.1" {
		t.Errorf("version = %q, want %q", issue.Version, "0.1")
	}
	if len(issue.Labels) != 2 || issue.Labels[0] != "bug" || issue.Labels[1] != "enhancement" {
		t.Errorf("labels = %v, want [bug enhancement]", issue.Labels)
	}
	if issue.Priority != "high" {
		t.Errorf("priority = %q, want %q", issue.Priority, "high")
	}
	if issue.Assignee != "alice" {
		t.Errorf("assignee = %q, want %q", issue.Assignee, "alice")
	}
	if issue.Slug != "my-issue" {
		t.Errorf("slug = %q, want %q", issue.Slug, "my-issue")
	}
	if !strings.Contains(issue.BodyHTML, "This is the body.") {
		t.Errorf("BodyHTML missing body text: %q", issue.BodyHTML)
	}
	if !strings.Contains(issue.BodyRaw, "This is the body.") {
		t.Errorf("BodyRaw missing body text: %q", issue.BodyRaw)
	}
}

func TestParseIssue_MissingFrontmatter(t *testing.T) {
	data := []byte("Just some text without frontmatter")
	_, err := ParseIssue("test.md", data)
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseIssue_StatusNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"In Progress", "in progress"},
		{"  DONE  ", "done"},
		{"Backlog", "backlog"},
		{"idea", "idea"},
	}

	for _, tt := range tests {
		data := []byte("---\ntitle: \"Test\"\nstatus: \"" + tt.input + "\"\n---\n\nbody")
		issue, err := ParseIssue("test.md", data)
		if err != nil {
			t.Fatalf("unexpected error for status %q: %v", tt.input, err)
		}
		if issue.Status != tt.want {
			t.Errorf("status %q normalized to %q, want %q", tt.input, issue.Status, tt.want)
		}
	}
}

func TestParseIssue_PriorityNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"High", "high"},
		{"  LOW  ", "low"},
		{"CRITICAL", "critical"},
	}

	for _, tt := range tests {
		data := []byte("---\ntitle: \"Test\"\npriority: \"" + tt.input + "\"\n---\n\nbody")
		issue, err := ParseIssue("test.md", data)
		if err != nil {
			t.Fatalf("unexpected error for priority %q: %v", tt.input, err)
		}
		if issue.Priority != tt.want {
			t.Errorf("priority %q normalized to %q, want %q", tt.input, issue.Priority, tt.want)
		}
	}
}

func TestParseIssue_EmptyLabels(t *testing.T) {
	data := []byte("---\ntitle: \"Test\"\n---\n\nbody")
	issue, err := ParseIssue("test.md", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.Labels != nil {
		t.Errorf("labels = %v, want nil", issue.Labels)
	}
}

func TestLoadIssues(t *testing.T) {
	dir := t.TempDir()

	// Create a root-level issue
	os.WriteFile(filepath.Join(dir, "root-issue.md"), []byte(`---
title: "Root Issue"
status: "idea"
---

Root body.
`), 0644)

	// Create a subdirectory with an issue
	subDir := filepath.Join(dir, "Combat")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "sub-issue.md"), []byte(`---
title: "Sub Issue"
status: "backlog"
system: "Combat"
---

Sub body.
`), 0644)

	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}

	// Check that subdirectory slug includes the directory prefix
	slugs := map[string]bool{}
	for _, iss := range issues {
		slugs[iss.Slug] = true
	}
	if !slugs["root-issue"] {
		t.Error("missing slug 'root-issue'")
	}
	if !slugs["combat/sub-issue"] {
		t.Errorf("missing slug 'combat/sub-issue', got slugs: %v", slugs)
	}
}

func TestLoadIssues_SlugCollision(t *testing.T) {
	dir := t.TempDir()

	// Two issues with the same title
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\ntitle: \"Same Title\"\n---\n\nbody a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.md"), []byte("---\ntitle: \"Same Title\"\n---\n\nbody b"), 0644)

	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}

	slugs := map[string]bool{}
	for _, iss := range issues {
		slugs[iss.Slug] = true
	}
	// One should be "same-title" and the other "same-title-2"
	if len(slugs) != 2 {
		t.Errorf("expected 2 unique slugs, got %v", slugs)
	}
}

func TestLoadIssues_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	issues, err := LoadIssues(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("got %d issues, want 0", len(issues))
	}
}

func TestUpdateIssueFrontmatter(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")

	original := `---
title: "Test Issue"
status: "idea"
priority: "low"
---

Body text.
`
	os.WriteFile(fp, []byte(original), 0644)

	newStatus := "in progress"
	newPriority := "high"
	newAssignee := "bob"
	newVersion := "1.0"
	err := UpdateIssueFrontmatter(fp, IssueUpdate{
		Status:   &newStatus,
		Priority: &newPriority,
		Assignee: &newAssignee,
		Version:  &newVersion,
		Labels:   []string{"bug", "ui"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	content := string(data)

	if !strings.Contains(content, "in progress") {
		t.Error("status not updated")
	}
	if !strings.Contains(content, "high") {
		t.Error("priority not updated")
	}
	if !strings.Contains(content, "bob") {
		t.Error("assignee not updated")
	}
	if !strings.Contains(content, "1.0") {
		t.Error("version not updated")
	}
	if !strings.Contains(content, "bug") || !strings.Contains(content, "ui") {
		t.Error("labels not updated")
	}
}

func TestUpdateIssueFrontmatter_ClearFields(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")

	original := `---
title: "Test"
priority: "high"
version: "1.0"
assignee: "alice"
labels:
  - bug
---

Body.
`
	os.WriteFile(fp, []byte(original), 0644)

	empty := ""
	err := UpdateIssueFrontmatter(fp, IssueUpdate{
		Priority: &empty,
		Version:  &empty,
		Assignee: &empty,
		Labels:   []string{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	content := string(data)

	// The cleared fields should be removed from frontmatter
	if strings.Contains(content, "priority") {
		t.Error("priority should have been removed")
	}
	if strings.Contains(content, "version") {
		t.Error("version should have been removed")
	}
	if strings.Contains(content, "assignee") {
		t.Error("assignee should have been removed")
	}
	if strings.Contains(content, "labels") {
		t.Error("labels should have been removed")
	}
}

func TestUpdateIssueFrontmatter_UpdateBody(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")

	original := `---
title: "Test"
---

Old body.
`
	os.WriteFile(fp, []byte(original), 0644)

	newBody := "New body content."
	err := UpdateIssueFrontmatter(fp, IssueUpdate{
		Body: &newBody,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(fp)
	content := string(data)
	if !strings.Contains(content, "New body content.") {
		t.Error("body not updated")
	}
}

func TestDeleteIssue(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	os.WriteFile(fp, []byte("---\ntitle: \"Test\"\n---\n"), 0644)

	err := DeleteIssue(fp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

func TestDeleteIssue_WithCommentSidecar(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	commentFp := filepath.Join(dir, "test.comments.yaml")
	os.WriteFile(fp, []byte("---\ntitle: \"Test\"\n---\n"), 0644)
	os.WriteFile(commentFp, []byte("some comments"), 0644)

	err := DeleteIssue(fp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Error("issue file should have been deleted")
	}
	if _, err := os.Stat(commentFp); !os.IsNotExist(err) {
		t.Error("comment sidecar should have been deleted")
	}
}

func TestCreateIssueFile(t *testing.T) {
	dir := t.TempDir()

	fp, slug, err := CreateIssueFile(dir, "My New Issue", "backlog", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if slug != "my-new-issue" {
		t.Errorf("slug = %q, want %q", slug, "my-new-issue")
	}

	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("failed to read created file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "My New Issue") {
		t.Error("file missing title")
	}
	if !strings.Contains(content, "backlog") {
		t.Error("file missing status")
	}
}

func TestCreateIssueFile_WithSystem(t *testing.T) {
	dir := t.TempDir()

	fp, slug, err := CreateIssueFile(dir, "System Issue", "idea", "Combat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if slug != "combat/system-issue" {
		t.Errorf("slug = %q, want %q", slug, "combat/system-issue")
	}
	if !strings.Contains(fp, "Combat") {
		t.Errorf("file path should contain system dir: %s", fp)
	}

	data, _ := os.ReadFile(fp)
	if !strings.Contains(string(data), `system: "Combat"`) {
		t.Error("file missing system field")
	}
}

func TestCreateIssueFile_EmptyTitle(t *testing.T) {
	dir := t.TempDir()
	_, _, err := CreateIssueFile(dir, "", "idea", "")
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestCreateIssueFile_DefaultStatus(t *testing.T) {
	dir := t.TempDir()
	fp, _, err := CreateIssueFile(dir, "Default Status", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(fp)
	if !strings.Contains(string(data), `status: "idea"`) {
		t.Error("default status should be 'idea'")
	}
}

func TestCollectFilterValues(t *testing.T) {
	issues := []*Issue{
		{Status: "idea", System: "Combat", Priority: "high", Labels: []string{"bug"}, Assignee: "alice"},
		{Status: "done", System: "UI", Priority: "low", Labels: []string{"bug", "ui"}, Assignee: "bob"},
		{Status: "idea", System: "Combat", Priority: "high", Labels: []string{"enhancement"}},
	}

	statuses, systems, priorities, labels, assignees := CollectFilterValues(issues)

	if len(statuses) != 2 {
		t.Errorf("statuses = %v, want 2 items", statuses)
	}
	if len(systems) != 2 {
		t.Errorf("systems = %v, want 2 items", systems)
	}
	if len(priorities) != 2 {
		t.Errorf("priorities = %v, want 2 items", priorities)
	}
	if len(labels) != 3 {
		t.Errorf("labels = %v, want 3 items", labels)
	}
	if len(assignees) != 2 {
		t.Errorf("assignees = %v, want 2 items", assignees)
	}
}

func TestStatusIndex(t *testing.T) {
	tests := []struct {
		status string
		want   int
	}{
		{"none", 0},
		{"idea", 1},
		{"done", 7},
		{"unknown", -1},
	}

	for _, tt := range tests {
		got := StatusIndex(tt.status)
		if got != tt.want {
			t.Errorf("StatusIndex(%q) = %d, want %d", tt.status, got, tt.want)
		}
	}
}

func TestValidTransition(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"idea", "in design", true},
		{"in progress", "testing", true},
		{"idea", "done", false},     // skip not allowed
		{"done", "idea", false},     // backwards not allowed
		{"unknown", "idea", false},  // unknown status
		{"idea", "unknown", false},  // unknown status
		{"none", "idea", true},      // first transition
		{"documentation", "done", true},
	}

	for _, tt := range tests {
		got := ValidTransition(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("ValidTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestCountCheckboxes(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		total   int
		checked int
	}{
		{"no checkboxes", "Some text", 0, 0},
		{"all unchecked", "- [ ] a\n- [ ] b", 2, 0},
		{"all checked", "- [x] a\n- [X] b", 2, 2},
		{"mixed", "- [x] done\n- [ ] todo\n- [X] also done", 3, 2},
		{"with indentation", "  - [ ] indented\n  - [x] indented checked", 2, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total, checked := CountCheckboxes(tt.body)
			if total != tt.total || checked != tt.checked {
				t.Errorf("CountCheckboxes() = (%d, %d), want (%d, %d)", total, checked, tt.total, tt.checked)
			}
		})
	}
}

func TestCheckCheckbox(t *testing.T) {
	body := "- [ ] implement feature\n- [ ] write tests\n- [x] create PR"

	t.Run("exact match", func(t *testing.T) {
		result, found := CheckCheckbox(body, "implement feature")
		if !found {
			t.Fatal("expected match")
		}
		if !strings.Contains(result, "- [x] implement feature") {
			t.Errorf("checkbox not checked: %s", result)
		}
	})

	t.Run("partial match", func(t *testing.T) {
		result, found := CheckCheckbox(body, "write")
		if !found {
			t.Fatal("expected match")
		}
		if !strings.Contains(result, "- [x] write tests") {
			t.Errorf("checkbox not checked: %s", result)
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, found := CheckCheckbox(body, "nonexistent")
		if found {
			t.Fatal("expected no match")
		}
	})

	t.Run("already checked not matched", func(t *testing.T) {
		_, found := CheckCheckbox(body, "create PR")
		if found {
			t.Fatal("already checked items should not be matched")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		_, found := CheckCheckbox(body, "IMPLEMENT")
		if !found {
			t.Fatal("expected case insensitive match")
		}
	})
}

func TestHasTestPlan(t *testing.T) {
	t.Run("with both sections", func(t *testing.T) {
		body := "Some content\n\n## Test Plan\n\n### Automated\nUnit tests\n\n### Manual\nClick around"
		hasAuto, hasManual := HasTestPlan(body)
		if !hasAuto || !hasManual {
			t.Errorf("HasTestPlan = (%v, %v), want (true, true)", hasAuto, hasManual)
		}
	})

	t.Run("missing automated", func(t *testing.T) {
		body := "## Test Plan\n\n### Manual\nTest steps"
		hasAuto, hasManual := HasTestPlan(body)
		if hasAuto {
			t.Error("expected hasAutomated = false")
		}
		if !hasManual {
			t.Error("expected hasManual = true")
		}
	})

	t.Run("no test plan", func(t *testing.T) {
		body := "Just some content\n\n## Other Section"
		hasAuto, hasManual := HasTestPlan(body)
		if hasAuto || hasManual {
			t.Errorf("HasTestPlan = (%v, %v), want (false, false)", hasAuto, hasManual)
		}
	})

	t.Run("test plan ended by another h2", func(t *testing.T) {
		body := "## Test Plan\n### Automated\nTests\n## Next Section\n### Manual\nSteps"
		hasAuto, hasManual := HasTestPlan(body)
		if !hasAuto {
			t.Error("expected hasAutomated = true")
		}
		if hasManual {
			t.Error("Manual is outside Test Plan section, should be false")
		}
	})
}

func TestHasCommentWithPrefix(t *testing.T) {
	comments := []Comment{
		{Text: "tests: all unit tests pass"},
		{Text: "docs: updated readme"},
		{Text: "just a comment"},
	}

	tests := []struct {
		prefix string
		want   bool
	}{
		{"tests:", true},
		{"docs:", true},
		{"just", true},
		{"TESTS:", true}, // case insensitive
		{"missing:", false},
	}

	for _, tt := range tests {
		got := HasCommentWithPrefix(comments, tt.prefix)
		if got != tt.want {
			t.Errorf("HasCommentWithPrefix(%q) = %v, want %v", tt.prefix, got, tt.want)
		}
	}
}
