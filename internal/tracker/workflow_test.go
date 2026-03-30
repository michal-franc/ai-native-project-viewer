package tracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultWorkflow(t *testing.T) {
	wf := DefaultWorkflow()

	if len(wf.Statuses) == 0 {
		t.Fatal("default workflow has no statuses")
	}

	order := wf.GetStatusOrder()
	if order[0] != "none" {
		t.Errorf("first status = %q, want %q", order[0], "none")
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

	expected := []string{"none", "idea", "in design", "backlog", "in progress", "testing", "documentation", "done"}
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
		{"none", 0},
		{"idea", 1},
		{"done", 7},
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
		{"idea", "done", false},       // skip
		{"done", "idea", false},       // backwards
		{"unknown", "idea", false},    // unknown from
		{"idea", "unknown", false},    // unknown to
		{"idea", "idea", false},       // same
		{"none", "idea", true},
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

	if descs["none"] != "" {
		t.Errorf("none description = %q, want empty", descs["none"])
	}
	if descs["backlog"] != "Ready to work on" {
		t.Errorf("backlog description = %q", descs["backlog"])
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
		issue := &Issue{BodyRaw: "- [ ] task 1"}
		err := wf.Validate(issue, "backlog", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_checkboxes fails", func(t *testing.T) {
		issue := &Issue{BodyRaw: "No checkboxes here"}
		err := wf.Validate(issue, "backlog", nil)
		if err == nil {
			t.Fatal("expected error")
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

	t.Run("has_test_plan passes", func(t *testing.T) {
		issue := &Issue{BodyRaw: "## Test Plan\n### Automated\nTests\n### Manual\nSteps"}
		comments := []Comment{{Text: "tests: all pass"}}
		err := wf.Validate(issue, "documentation", comments)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_test_plan fails", func(t *testing.T) {
		issue := &Issue{BodyRaw: "No test plan"}
		comments := []Comment{{Text: "tests: all pass"}}
		err := wf.Validate(issue, "documentation", comments)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("has_comment_prefix passes", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content"}
		comments := []Comment{{Text: "docs: updated docs"}}
		err := wf.Validate(issue, "done", comments)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("has_comment_prefix fails", func(t *testing.T) {
		issue := &Issue{BodyRaw: "content"}
		comments := []Comment{{Text: "some other comment"}}
		err := wf.Validate(issue, "done", comments)
		if err == nil {
			t.Fatal("expected error")
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

func TestNextStatus(t *testing.T) {
	wf := DefaultWorkflow()

	tests := []struct {
		current string
		want    string
	}{
		{"none", "idea"},
		{"idea", "in design"},
		{"documentation", "done"},
		{"done", ""},
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
    validation:
      - body_not_empty
  - name: "doing"
    description: "In progress"
  - name: "done"
    description: "Complete"
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

	if len(wf.Statuses[0].Validation) != 1 || wf.Statuses[0].Validation[0] != "body_not_empty" {
		t.Errorf("validation = %v, want [body_not_empty]", wf.Statuses[0].Validation)
	}
}

func TestLoadWorkflow_InvalidFile(t *testing.T) {
	_, err := LoadWorkflow("/nonexistent/path/workflow.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}
