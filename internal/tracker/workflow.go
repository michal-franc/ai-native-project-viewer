package tracker

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type WorkflowStatus struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Template    string   `yaml:"template"`
	Validation  []string `yaml:"validation"`
}

type WorkflowConfig struct {
	Statuses []WorkflowStatus `yaml:"statuses"`
}

func LoadWorkflow(path string) (*WorkflowConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading workflow %s: %w", path, err)
	}

	var cfg WorkflowConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing workflow %s: %w", path, err)
	}

	return &cfg, nil
}

func DefaultWorkflow() *WorkflowConfig {
	return &WorkflowConfig{
		Statuses: []WorkflowStatus{
			{Name: "idea", Description: "Raw idea, needs exploration"},
			{
				Name:        "in design",
				Description: "Being designed and specced out",
				Validation:  []string{"body_not_empty"},
			},
			{
				Name:        "backlog",
				Description: "Ready to work on",
				Validation:  []string{"has_checkboxes"},
			},
			{
				Name:        "in progress",
				Description: "Actively being implemented",
				Validation:  []string{"has_assignee"},
			},
			{
				Name:        "testing",
				Description: "Under verification",
				Validation:  []string{"all_checkboxes_checked"},
			},
			{
				Name:        "human-testing",
				Description: "Manual verification by humans",
				Validation:  []string{"has_test_plan", "has_comment_prefix: tests:"},
			},
			{
				Name:        "documentation",
				Description: "Being documented",
			},
			{
				Name:        "done",
				Description: "Completed",
				Validation:  []string{"has_comment_prefix: docs:"},
			},
		},
	}
}

func (w *WorkflowConfig) GetStatusOrder() []string {
	order := make([]string, len(w.Statuses))
	for i, s := range w.Statuses {
		order[i] = s.Name
	}
	return order
}

func (w *WorkflowConfig) GetStatusDescriptions() map[string]string {
	descs := make(map[string]string, len(w.Statuses))
	for _, s := range w.Statuses {
		descs[s.Name] = s.Description
	}
	return descs
}

func (w *WorkflowConfig) GetStatusIndex(status string) int {
	for i, s := range w.Statuses {
		if s.Name == status {
			return i
		}
	}
	return -1
}

func (w *WorkflowConfig) IsValidTransition(from, to string) bool {
	fi := w.GetStatusIndex(from)
	ti := w.GetStatusIndex(to)
	if fi == -1 || ti == -1 {
		return false
	}
	return ti == fi+1
}

func (w *WorkflowConfig) GetStatus(name string) *WorkflowStatus {
	for i := range w.Statuses {
		if w.Statuses[i].Name == name {
			return &w.Statuses[i]
		}
	}
	return nil
}

func (w *WorkflowConfig) TemplateForStatus(name string) string {
	s := w.GetStatus(name)
	if s == nil {
		return ""
	}
	return strings.TrimRight(s.Template, "\n")
}

// AppendTemplate appends the status template to the body if not already present.
// Returns the new body and whether anything was appended.
func (w *WorkflowConfig) AppendTemplate(body, status string) (string, bool) {
	tmpl := w.TemplateForStatus(status)
	if tmpl == "" {
		return body, false
	}

	// Duplicate guard: check if the first line (section heading) already exists
	firstLine := strings.SplitN(tmpl, "\n", 2)[0]
	if strings.Contains(body, firstLine) {
		return body, false
	}

	body = strings.TrimRight(body, "\n")
	if body != "" {
		body += "\n\n"
	}
	body += tmpl + "\n"
	return body, true
}

// Validate checks whether an issue meets all validation rules for transitioning
// into the given status. Returns nil if valid, or an error describing what's missing.
func (w *WorkflowConfig) Validate(issue *Issue, toStatus string, comments []Comment) error {
	s := w.GetStatus(toStatus)
	if s == nil {
		return fmt.Errorf("unknown status %q", toStatus)
	}

	for _, rule := range s.Validation {
		if err := w.checkRule(rule, issue, comments); err != nil {
			return err
		}
	}
	return nil
}

func (w *WorkflowConfig) checkRule(rule string, issue *Issue, comments []Comment) error {
	// Parse "rule_name: arg" format
	ruleName := rule
	ruleArg := ""
	if idx := strings.Index(rule, ": "); idx != -1 {
		ruleName = rule[:idx]
		ruleArg = rule[idx+2:]
	}

	switch ruleName {
	case "body_not_empty":
		if strings.TrimSpace(issue.BodyRaw) == "" {
			return fmt.Errorf("issue body is empty — add a description first")
		}
	case "has_checkboxes":
		total, _ := CountCheckboxes(issue.BodyRaw)
		if total == 0 {
			return fmt.Errorf("no checkboxes found — add acceptance criteria as checkboxes:\n\n  - [ ] First requirement\n  - [ ] Second requirement")
		}
	case "has_assignee":
		if issue.Assignee == "" {
			return fmt.Errorf("no assignee — claim the issue first:\n\n  issue-cli claim %s --assignee \"your-name\"", issue.Slug)
		}
	case "all_checkboxes_checked":
		total, checked := CountCheckboxes(issue.BodyRaw)
		if total > 0 && checked < total {
			return fmt.Errorf("%d/%d checkboxes incomplete:\n\n  issue-cli checklist %s", checked, total, issue.Slug)
		}
	case "section_checkboxes_checked":
		if ruleArg == "" {
			return fmt.Errorf("section_checkboxes_checked rule requires a section name argument")
		}
		total, checked := CountCheckboxesInSection(issue.BodyRaw, ruleArg)
		if total == 0 {
			// Section missing or has no checkboxes — skip silently.
			// The section may not exist if the issue was created without that template.
			return nil
		}
		if checked < total {
			return fmt.Errorf("%d/%d checkboxes incomplete in section %q:\n\n  issue-cli checklist %s", checked, total, ruleArg, issue.Slug)
		}
	case "has_test_plan":
		hasAuto, hasManual := HasTestPlan(issue.BodyRaw)
		if !hasAuto || !hasManual {
			return fmt.Errorf("missing test plan — add a ## Test Plan section with ### Automated and ### Manual subsections")
		}
	case "has_comment_prefix":
		if ruleArg == "" {
			return fmt.Errorf("has_comment_prefix rule requires an argument")
		}
		if !HasCommentWithPrefix(comments, ruleArg) {
			return fmt.Errorf("no comment starting with %q — add one:\n\n  issue-cli comment %s --text \"%s ...\"", ruleArg, issue.Slug, ruleArg)
		}
	default:
		return fmt.Errorf("unknown validation rule: %s", ruleName)
	}
	return nil
}

// NextStatus returns the status name that follows the given one, or empty string.
func (w *WorkflowConfig) NextStatus(current string) string {
	idx := w.GetStatusIndex(current)
	if idx == -1 || idx+1 >= len(w.Statuses) {
		return ""
	}
	return w.Statuses[idx+1].Name
}
