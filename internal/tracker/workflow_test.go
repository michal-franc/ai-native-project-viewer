package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultWorkflow(t *testing.T) {
	wf := DefaultWorkflow()

	if len(wf.Statuses) == 0 {
		t.Fatal("default workflow has no statuses")
	}

	order := wf.GetStatusOrder()
	if order[0] != "idea" {
		t.Errorf("first status = %q, want %q", order[0], "idea")
	}
	if order[len(order)-1] != "done" {
		t.Errorf("last status = %q, want %q", order[len(order)-1], "done")
	}

	descs := wf.GetStatusDescriptions()
	if descs["idea"] != "Raw idea, needs exploration" {
		t.Errorf("idea description = %q", descs["idea"])
	}
}

func TestGetStatusOrder(t *testing.T) {
	wf := DefaultWorkflow()
	order := wf.GetStatusOrder()

	expected := []string{"idea", "in design", "backlog", "in progress", "testing", "human-testing", "documentation", "done"}
	if len(order) != len(expected) {
		t.Fatalf("got %d statuses, want %d", len(order), len(expected))
	}
	for i, s := range expected {
		if order[i] != s {
			t.Errorf("order[%d] = %q, want %q", i, order[i], s)
		}
	}
}

func TestGetStatusIndex(t *testing.T) {
	wf := DefaultWorkflow()

	tests := []struct {
		status string
		want   int
	}{
		{"idea", 0},
		{"in design", 1},
		{"human-testing", 5},
		{"done", 7},
		{"none", -1},
		{"unknown", -1},
	}

	for _, tt := range tests {
		got := wf.GetStatusIndex(tt.status)
		if got != tt.want {
			t.Errorf("GetStatusIndex(%q) = %d, want %d", tt.status, got, tt.want)
		}
	}
}

func TestIsValidTransition(t *testing.T) {
	wf := DefaultWorkflow()

	tests := []struct {
		from string
		to   string
		want bool
	}{
		{"idea", "in design", true},
		{"in progress", "testing", true},
		{"idea", "done", false},           // skip
		{"done", "idea", false},           // backwards
		{"unknown", "idea", false},        // unknown from
		{"idea", "unknown", false},        // unknown to
		{"idea", "idea", false},           // same
		{"none", "idea", false},           // none no longer exists
		{"testing", "human-testing", true},
		{"human-testing", "documentation", true},
		{"documentation", "done", true},
	}

	for _, tt := range tests {
		got := wf.IsValidTransition(tt.from, tt.to)
		if got != tt.want {
			t.Errorf("IsValidTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestGetStatusDescriptions(t *testing.T) {
	wf := DefaultWorkflow()
	descs := wf.GetStatusDescriptions()

	if _, ok := descs["none"]; ok {
		t.Errorf("none should not be in descriptions")
	}
	if descs["backlog"] != "Ready to work on" {
		t.Errorf("backlog description = %q", descs["backlog"])
	}
	if descs["human-testing"] != "Manual verification by humans" {
		t.Errorf("human-testing description = %q", descs["human-testing"])
	}
}

func TestTemplateForStatus(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "idea", Template: "## Ideas\n- Item 1\n"},
			{Name: "done", Template: ""},
		},
	}

	t.Run("existing with template", func(t *testing.T) {
		tmpl := wf.TemplateForStatus("idea")
		if tmpl != "## Ideas\n- Item 1" {
			t.Errorf("template = %q", tmpl)
		}
	})

	t.Run("existing without template", func(t *testing.T) {
		tmpl := wf.TemplateForStatus("done")
		if tmpl != "" {
			t.Errorf("template = %q, want empty", tmpl)
		}
	})

	t.Run("missing status", func(t *testing.T) {
		tmpl := wf.TemplateForStatus("unknown")
		if tmpl != "" {
			t.Errorf("template = %q, want empty", tmpl)
		}
	})
}

func TestAppendTemplate(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "testing", Template: "## Test Plan\n- [ ] Test 1\n"},
			{Name: "idea", Template: ""},
		},
	}

	t.Run("append to body", func(t *testing.T) {
		body, appended := wf.AppendTemplate("Existing content", "testing")
		if !appended {
			t.Fatal("expected template to be appended")
		}
		if body != "Existing content\n\n## Test Plan\n- [ ] Test 1\n" {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("append to empty body", func(t *testing.T) {
		body, appended := wf.AppendTemplate("", "testing")
		if !appended {
			t.Fatal("expected template to be appended")
		}
		if body != "## Test Plan\n- [ ] Test 1\n" {
			t.Errorf("body = %q", body)
		}
	})

	t.Run("duplicate guard", func(t *testing.T) {
		body, appended := wf.AppendTemplate("## Test Plan\nAlready here", "testing")
		if appended {
			t.Fatal("should not append duplicate")
		}
		if body != "## Test Plan\nAlready here" {
			t.Errorf("body changed: %q", body)
		}
	})

	t.Run("no template for status", func(t *testing.T) {
		body, appended := wf.AppendTemplate("content", "idea")
		if appended {
			t.Fatal("should not append empty template")
		}
		if body != "content" {
			t.Errorf("body = %q", body)
		}
	})
}

func TestAppendToSection(t *testing.T) {
	t.Run("creates section when missing", func(t *testing.T) {
		body, changed := appendToSection("## Existing\n- item", "Testing", "- [ ] add test")
		if !changed {
			t.Fatal("expected section append")
		}
		want := "## Existing\n- item\n\n## Testing\n- [ ] add test\n"
		if body != want {
			t.Errorf("body = %q, want %q", body, want)
		}
	})

	t.Run("reuses existing section", func(t *testing.T) {
		body, changed := appendToSection("## Testing\n- [ ] existing", "Testing", "- [ ] new item")
		if !changed {
			t.Fatal("expected section reuse append")
		}
		want := "## Testing\n- [ ] existing\n\n- [ ] new item"
		if body != want {
			t.Errorf("body = %q, want %q", body, want)
		}
	})

	t.Run("does not duplicate identical content", func(t *testing.T) {
		body, changed := appendToSection("## Testing\n- [ ] same", "Testing", "- [ ] same")
		if changed {
			t.Fatal("expected no change for duplicate content")
		}
		if body != "## Testing\n- [ ] same" {
			t.Errorf("body changed: %q", body)
		}
	})
}

func TestValidate(t *testing.T) {
	wf := DefaultWorkflow()

	t.Run("body_not_empty passes", func(t *testing.T) {
		issue := &Issue{BodyRaw: "Some content"}
		err := wf.Validate(issue, "in design", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("body_not_empty fails", func(t *testing.T) {
		issue := &Issue{BodyRaw: ""}
		err := wf.Validate(issue, "in design", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("has_checkboxes passes", func(t *testing.T) {
		issue := &Issue{BodyRaw: "- [ ] task 1", ApprovedFor: "backlog"}
		err := wf.Validate(issue, "backlog", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_checkboxes fails", func(t *testing.T) {
		issue := &Issue{BodyRaw: "No checkboxes here", ApprovedFor: "backlog"}
		err := wf.Validate(issue, "backlog", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("approved_for blocks backlog", func(t *testing.T) {
		issue := &Issue{BodyRaw: "- [ ] task 1"}
		err := wf.Validate(issue, "backlog", nil)
		if err == nil {
			t.Fatal("expected error for missing approval")
		}
	})

	t.Run("approved_for wrong status", func(t *testing.T) {
		issue := &Issue{BodyRaw: "- [ ] task 1", ApprovedFor: "testing"}
		err := wf.Validate(issue, "backlog", nil)
		if err == nil {
			t.Fatal("expected error for wrong approval status")
		}
	})

	t.Run("has_assignee passes", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content", Assignee: "alice"}
		err := wf.Validate(issue, "in progress", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_assignee fails", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content", Assignee: ""}
		err := wf.Validate(issue, "in progress", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("all_checkboxes_checked passes", func(t *testing.T) {
		issue := &Issue{BodyRaw: "- [x] done 1\n- [x] done 2"}
		err := wf.Validate(issue, "testing", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("all_checkboxes_checked fails", func(t *testing.T) {
		issue := &Issue{BodyRaw: "- [x] done\n- [ ] not done"}
		err := wf.Validate(issue, "testing", nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("all_checkboxes_checked passes when no checkboxes", func(t *testing.T) {
		issue := &Issue{BodyRaw: "No checkboxes"}
		err := wf.Validate(issue, "testing", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("section_checkboxes_checked passes", func(t *testing.T) {
		sectionWf := &WorkflowConfig{
			Statuses: []WorkflowStatus{
				{Name: "testing", Validation: []string{"section_checkboxes_checked: Implementation"}},
			},
		}
		issue := &Issue{
			Slug:    "test",
			BodyRaw: "## Implementation\n- [x] Code done\n- [x] Tests written\n\n## Testing\n- [ ] Not done yet",
		}
		err := sectionWf.Validate(issue, "testing", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("section_checkboxes_checked fails with unchecked", func(t *testing.T) {
		sectionWf := &WorkflowConfig{
			Statuses: []WorkflowStatus{
				{Name: "testing", Validation: []string{"section_checkboxes_checked: Implementation"}},
			},
		}
		issue := &Issue{
			Slug:    "test",
			BodyRaw: "## Implementation\n- [x] Code done\n- [ ] Tests written\n\n## Testing\n- [ ] Not done yet",
		}
		err := sectionWf.Validate(issue, "testing", nil)
		if err == nil {
			t.Fatal("expected error for unchecked section checkbox")
		}
	})

	t.Run("section_checkboxes_checked skips missing section", func(t *testing.T) {
		sectionWf := &WorkflowConfig{
			Statuses: []WorkflowStatus{
				{Name: "testing", Validation: []string{"section_checkboxes_checked: Implementation"}},
			},
		}
		issue := &Issue{
			Slug:    "test",
			BodyRaw: "## Other\n- [x] Done",
		}
		err := sectionWf.Validate(issue, "testing", nil)
		if err != nil {
			t.Errorf("expected no error for missing section, got: %v", err)
		}
	})

	t.Run("has_test_plan passes for human-testing", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Test Plan\n### Automated\nTests\n### Manual\nSteps"}
		comments := []Comment{{Text: "tests: all pass"}}
		err := wf.Validate(issue, "human-testing", comments)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_test_plan fails for human-testing", func(t *testing.T) {
		issue := &Issue{BodyRaw: "No test plan"}
		comments := []Comment{{Text: "tests: all pass"}}
		err := wf.Validate(issue, "human-testing", comments)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("has_comment_prefix tests: fails for human-testing", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Test Plan\n### Automated\nTests\n### Manual\nSteps"}
		comments := []Comment{{Text: "some other comment"}}
		err := wf.Validate(issue, "human-testing", comments)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("documentation requires approval", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content"}
		err := wf.Validate(issue, "documentation", nil)
		if err == nil {
			t.Fatal("expected error for missing approval")
		}
	})

	t.Run("documentation passes with approval", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content", ApprovedFor: "documentation"}
		err := wf.Validate(issue, "documentation", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_comment_prefix docs: passes for done", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content", ApprovedFor: "done"}
		comments := []Comment{{Text: "docs: updated docs"}}
		err := wf.Validate(issue, "done", comments)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_comment_prefix docs: fails for done", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content", ApprovedFor: "done"}
		comments := []Comment{{Text: "some other comment"}}
		err := wf.Validate(issue, "done", comments)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("done requires approval", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content"}
		comments := []Comment{{Text: "docs: updated docs"}}
		err := wf.Validate(issue, "done", comments)
		if err == nil {
			t.Fatal("expected error for missing approval")
		}
	})

	t.Run("unknown status", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content"}
		err := wf.Validate(issue, "nonexistent", nil)
		if err == nil {
			t.Fatal("expected error for unknown status")
		}
	})
}

func TestValidateTransition_WithActions(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "backlog"},
			{Name: "in progress"},
		},
		Transitions: []WorkflowTransition{
			{
				From: "backlog",
				To:   "in progress",
				Actions: []WorkflowAction{
					{Type: "validate", Rule: "has_assignee"},
					{Type: "require_human_approval", Status: "in progress"},
				},
			},
		},
	}

	err := wf.ValidateTransition(&Issue{Slug: "x", Assignee: "alice"}, "backlog", "in progress", nil)
	if err == nil {
		t.Fatal("expected missing approval to fail")
	}

	err = wf.ValidateTransition(&Issue{Slug: "x", Assignee: "alice", ApprovedFor: "in progress"}, "backlog", "in progress", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyTransition(t *testing.T) {
	wf := &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "backlog"},
			{Name: "in progress"},
		},
		Transitions: []WorkflowTransition{
			{
				From: "backlog",
				To:   "in progress",
				Actions: []WorkflowAction{
					{Type: "append_section", Title: "Implementation", Body: "- [ ] Code complete"},
					{Type: "inject_prompt", Prompt: "Implement carefully"},
					{Type: "set_fields", Field: "assignee", Value: ""},
				},
			},
		},
	}

	issue := &Issue{BodyRaw: "Existing", ApprovedFor: "in progress"}
	result := wf.ApplyTransition(issue, "backlog", "in progress")

	if result.Update.Status == nil || *result.Update.Status != "in progress" {
		t.Fatalf("status update = %#v", result.Update.Status)
	}
	if result.Update.Assignee == nil || *result.Update.Assignee != "" {
		t.Fatalf("assignee update = %#v", result.Update.Assignee)
	}
	if result.Update.ApprovedFor == nil || *result.Update.ApprovedFor != "" {
		t.Fatalf("approved_for update = %#v", result.Update.ApprovedFor)
	}
	if result.Update.Body == nil || !strings.Contains(*result.Update.Body, "## Implementation") {
		t.Fatalf("body update missing implementation section: %#v", result.Update.Body)
	}
	if len(result.InjectedPrompts) != 1 || result.InjectedPrompts[0] != "Implement carefully" {
		t.Fatalf("unexpected prompts: %#v", result.InjectedPrompts)
	}
}

func TestNextStatus(t *testing.T) {
	wf := DefaultWorkflow()

	tests := []struct {
		current string
		want    string
	}{
		{"idea", "in design"},
		{"testing", "human-testing"},
		{"human-testing", "documentation"},
		{"documentation", "done"},
		{"done", ""},
		{"none", ""},
		{"unknown", ""},
	}

	for _, tt := range tests {
		got := wf.NextStatus(tt.current)
		if got != tt.want {
			t.Errorf("NextStatus(%q) = %q, want %q", tt.current, got, tt.want)
		}
	}
}

func TestLoadWorkflow(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "workflow.yaml")

	content := `statuses:
  - name: "todo"
    description: "To do"
  - name: "doing"
    description: "In progress"
  - name: "done"
    description: "Complete"
transitions:
  - from: "todo"
    to: "doing"
    actions:
      - type: validate
        rule: body_not_empty
systems:
  Combat:
    transitions:
      - from: "doing"
        to: "done"
        actions:
          - type: inject_prompt
            prompt: "Combat-specific guidance"
`
	os.WriteFile(fp, []byte(content), 0644)

	wf, err := LoadWorkflow(fp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(wf.Statuses) != 3 {
		t.Fatalf("got %d statuses, want 3", len(wf.Statuses))
	}

	if wf.Statuses[0].Name != "todo" {
		t.Errorf("first status = %q, want %q", wf.Statuses[0].Name, "todo")
	}

	if len(wf.Transitions) != 1 {
		t.Fatalf("got %d transitions, want 1", len(wf.Transitions))
	}
	if wf.Transitions[0].Actions[0].Rule != "body_not_empty" {
		t.Errorf("rule = %q, want %q", wf.Transitions[0].Actions[0].Rule, "body_not_empty")
	}
	if _, ok := wf.Systems["Combat"]; !ok {
		t.Fatal("expected Combat system overlay")
	}
}

func TestLoadWorkflow_InvalidFile(t *testing.T) {
	_, err := LoadWorkflow("/nonexistent/path/workflow.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}
