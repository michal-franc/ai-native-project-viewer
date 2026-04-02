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
	SideEffects []string `yaml:"side_effects"`
}

type WorkflowAction struct {
	Type   string `yaml:"type"`
	Rule   string `yaml:"rule,omitempty"`
	Status string `yaml:"status,omitempty"`
	Title  string `yaml:"title,omitempty"`
	Body   string `yaml:"body,omitempty"`
	Prompt string `yaml:"prompt,omitempty"`
	Field  string `yaml:"field,omitempty"`
	Value  string `yaml:"value,omitempty"`
}

type WorkflowTransition struct {
	From    string           `yaml:"from"`
	To      string           `yaml:"to"`
	Actions []WorkflowAction `yaml:"actions"`
}

type WorkflowOverlay struct {
	Statuses    []WorkflowStatus     `yaml:"statuses"`
	Transitions []WorkflowTransition `yaml:"transitions"`
}

type WorkflowConfig struct {
	Statuses    []WorkflowStatus            `yaml:"statuses"`
	Transitions []WorkflowTransition        `yaml:"transitions"`
	Systems     map[string]WorkflowOverlay  `yaml:"systems"`
}

type TransitionResult struct {
	Update            IssueUpdate
	BodyChanged       bool
	BodyAppended      bool
	ClearedApproval   bool
	InjectedPrompts   []string
}

func stringPtr(s string) *string {
	v := s
	return &v
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
			{
				Name:        "idea",
				Description: "Raw idea, needs exploration",
			},
			{
				Name:        "in design",
				Description: "Being designed and specced out",
			},
			{
				Name:        "backlog",
				Description: "Ready to work on",
			},
			{
				Name:        "in progress",
				Description: "Actively being implemented",
			},
			{
				Name:        "testing",
				Description: "Under verification",
			},
			{
				Name:        "human-testing",
				Description: "Manual verification by humans",
			},
			{
				Name:        "documentation",
				Description: "Being documented",
			},
			{
				Name:        "done",
				Description: "Completed",
			},
		},
		Transitions: []WorkflowTransition{
			{
				From: "idea",
				To:   "in design",
				Actions: []WorkflowAction{
					{Type: "validate", Rule: "body_not_empty"},
					{Type: "append_section", Title: "Idea", Body: "- [ ] Problem described clearly\n- [ ] Scope defined\n- [ ] Constraints and open questions captured"},
					{Type: "append_section", Title: "Design", Body: "- [ ] Acceptance criteria defined as checkboxes\n- [ ] Approach documented\n- [ ] Open questions called out explicitly"},
				},
			},
			{
				From: "in design",
				To:   "backlog",
				Actions: []WorkflowAction{
					{Type: "validate", Rule: "has_checkboxes"},
					{Type: "require_human_approval", Status: "backlog"},
					{Type: "set_fields", Field: "assignee", Value: ""},
				},
			},
			{
				From: "backlog",
				To:   "in progress",
				Actions: []WorkflowAction{
					{Type: "validate", Rule: "has_assignee"},
					{Type: "append_section", Title: "Implementation", Body: "- [ ] Code changes complete\n- [ ] Tests added or updated"},
					{Type: "append_section", Title: "Test Plan", Body: "### Automated\n- [ ] Automated verification recorded\n\n### Manual\n- [ ] Manual verification steps listed if needed"},
					{Type: "inject_prompt", Prompt: "Implement the issue, update the Implementation section as work progresses, and keep the test plan concrete."},
				},
			},
			{
				From: "in progress",
				To:   "testing",
				Actions: []WorkflowAction{
					{Type: "validate", Rule: "all_checkboxes_checked"},
					{Type: "append_section", Title: "Testing", Body: "- [ ] Automated tests passing\n- [ ] Test results logged with `issue-cli comment <slug> --text \"tests: ...\"`"},
					{Type: "inject_prompt", Prompt: "Verify the implementation, run the relevant tests, and record the results in a `tests:` comment before continuing."},
				},
			},
			{
				From: "testing",
				To:   "human-testing",
				Actions: []WorkflowAction{
					{Type: "validate", Rule: "has_test_plan"},
					{Type: "validate", Rule: "has_comment_prefix: tests:"},
					{Type: "append_section", Title: "Human Testing", Body: "- [ ] Manual verification completed by a human if required"},
				},
			},
			{
				From: "human-testing",
				To:   "documentation",
				Actions: []WorkflowAction{
					{Type: "require_human_approval", Status: "documentation"},
					{Type: "append_section", Title: "Documentation", Body: "- [ ] User-facing docs updated if behavior changed\n- [ ] `docs:` comment prepared with the documentation changes"},
				},
			},
			{
				From: "documentation",
				To:   "done",
				Actions: []WorkflowAction{
					{Type: "validate", Rule: "has_comment_prefix: docs:"},
					{Type: "require_human_approval", Status: "done"},
				},
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

func (w *WorkflowConfig) GetTransition(from, to string) *WorkflowTransition {
	for i := range w.Transitions {
		t := &w.Transitions[i]
		if t.From == from && t.To == to {
			return t
		}
	}
	return nil
}

func (w *WorkflowConfig) Clone() *WorkflowConfig {
	if w == nil {
		return nil
	}

	clone := &WorkflowConfig{
		Statuses:    append([]WorkflowStatus(nil), w.Statuses...),
		Transitions: make([]WorkflowTransition, len(w.Transitions)),
		Systems:     make(map[string]WorkflowOverlay, len(w.Systems)),
	}
	for i := range w.Transitions {
		clone.Transitions[i] = WorkflowTransition{
			From:    w.Transitions[i].From,
			To:      w.Transitions[i].To,
			Actions: append([]WorkflowAction(nil), w.Transitions[i].Actions...),
		}
	}
	for name, overlay := range w.Systems {
		clonedOverlay := WorkflowOverlay{
			Statuses:    append([]WorkflowStatus(nil), overlay.Statuses...),
			Transitions: make([]WorkflowTransition, len(overlay.Transitions)),
		}
		for i := range overlay.Transitions {
			clonedOverlay.Transitions[i] = WorkflowTransition{
				From:    overlay.Transitions[i].From,
				To:      overlay.Transitions[i].To,
				Actions: append([]WorkflowAction(nil), overlay.Transitions[i].Actions...),
			}
		}
		clone.Systems[name] = clonedOverlay
	}
	return clone
}

func (w *WorkflowConfig) ForSystem(system string) *WorkflowConfig {
	if w == nil {
		return nil
	}
	if strings.TrimSpace(system) == "" || len(w.Systems) == 0 {
		return w.Clone()
	}

	overlay, ok := w.Systems[system]
	if !ok {
		return w.Clone()
	}

	merged := w.Clone()
	merged.Merge(&WorkflowConfig{
		Statuses:    overlay.Statuses,
		Transitions: overlay.Transitions,
	})
	return merged
}

func (w *WorkflowConfig) TemplateForStatus(name string) string {
	s := w.GetStatus(name)
	if s == nil {
		return ""
	}
	return strings.TrimRight(s.Template, "\n")
}

func (w *WorkflowConfig) transitionActions(from, to string) []WorkflowAction {
	if t := w.GetTransition(from, to); t != nil {
		return append([]WorkflowAction(nil), t.Actions...)
	}

	s := w.GetStatus(to)
	if s == nil {
		return nil
	}

	var actions []WorkflowAction
	for _, rule := range s.Validation {
		if strings.HasPrefix(rule, "approved_for: ") {
			actions = append(actions, WorkflowAction{
				Type:   "require_human_approval",
				Status: strings.TrimSpace(strings.TrimPrefix(rule, "approved_for: ")),
			})
			continue
		}
		actions = append(actions, WorkflowAction{Type: "validate", Rule: rule})
	}
	for _, effect := range s.SideEffects {
		switch effect {
		case "clear_assignee":
			actions = append(actions, WorkflowAction{Type: "set_fields", Field: "assignee", Value: ""})
		}
	}
	return actions
}

func (w *WorkflowConfig) TransitionPrompts(from, to string) []string {
	var prompts []string
	for _, action := range w.transitionActions(from, to) {
		if action.Type == "inject_prompt" && strings.TrimSpace(action.Prompt) != "" {
			prompts = append(prompts, strings.TrimSpace(action.Prompt))
		}
	}
	return prompts
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

func normalizeHeading(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	if strings.HasPrefix(title, "#") {
		return title
	}
	return "## " + title
}

func appendToSection(body, title, content string) (string, bool) {
	heading := normalizeHeading(title)
	content = strings.TrimSpace(content)
	if heading == "" || content == "" {
		return body, false
	}

	if !strings.Contains(body, heading) {
		body = strings.TrimRight(body, "\n")
		if body != "" {
			body += "\n\n"
		}
		body += heading + "\n" + content + "\n"
		return body, true
	}

	lines := strings.Split(body, "\n")
	headingLine := strings.TrimSpace(heading)
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == headingLine {
			start = i
			break
		}
	}
	if start == -1 {
		return body, false
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}

	section := strings.Join(lines[start:end], "\n")
	if strings.Contains(section, content) {
		return body, false
	}

	insert := []string{content}
	if end > start+1 && strings.TrimSpace(lines[end-1]) != "" {
		insert = append([]string{""}, insert...)
	}
	newLines := append([]string{}, lines[:end]...)
	newLines = append(newLines, insert...)
	newLines = append(newLines, lines[end:]...)
	return strings.Join(newLines, "\n"), true
}

// ValidateTransition checks whether an issue meets all validation rules for a transition.
// Returns nil if valid, or an error describing what's missing.
func (w *WorkflowConfig) ValidateTransition(issue *Issue, fromStatus, toStatus string, comments []Comment) error {
	if w.GetStatus(toStatus) == nil {
		return fmt.Errorf("unknown status %q", toStatus)
	}

	for _, action := range w.transitionActions(fromStatus, toStatus) {
		switch action.Type {
		case "validate":
			if err := w.checkRule(action.Rule, issue, comments); err != nil {
				return err
			}
		case "require_human_approval":
			status := strings.TrimSpace(action.Status)
			if status == "" {
				status = toStatus
			}
			if !strings.EqualFold(issue.ApprovedFor, status) {
				return fmt.Errorf("issue not approved for %q — a human must approve it first:\n\n  issue-cli update %s --approved-for %s", status, issue.Slug, status)
			}
		}
	}
	return nil
}

// Validate is kept for compatibility with older call sites and tests.
func (w *WorkflowConfig) Validate(issue *Issue, toStatus string, comments []Comment) error {
	fromStatus := ""
	if idx := w.GetStatusIndex(toStatus); idx > 0 {
		fromStatus = w.Statuses[idx-1].Name
	}
	return w.ValidateTransition(issue, fromStatus, toStatus, comments)
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
			return fmt.Errorf("missing test plan — add one with:\n  issue-cli append <slug> --body \"## Test Plan\\n\\n### Automated\\n- test description\\n\\n### Manual\\n- verification step\"")
		}
	case "has_comment_prefix":
		if ruleArg == "" {
			return fmt.Errorf("has_comment_prefix rule requires an argument")
		}
		if !HasCommentWithPrefix(comments, ruleArg) {
			return fmt.Errorf("no comment starting with %q — add one:\n\n  issue-cli comment %s --text \"%s ...\"", ruleArg, issue.Slug, ruleArg)
		}
	case "approved_for":
		if ruleArg == "" {
			return fmt.Errorf("approved_for rule requires a status argument")
		}
		if !strings.EqualFold(issue.ApprovedFor, ruleArg) {
			return fmt.Errorf("issue not approved for %q — a human must approve it first:\n\n  issue-cli update %s --approved-for %s", ruleArg, issue.Slug, ruleArg)
		}
	default:
		return fmt.Errorf("unknown validation rule: %s", ruleName)
	}
	return nil
}

func (w *WorkflowConfig) ApplyTransition(issue *Issue, fromStatus, toStatus string) TransitionResult {
	result := TransitionResult{
		Update: IssueUpdate{
			Status: stringPtr(toStatus),
		},
	}

	for _, action := range w.transitionActions(fromStatus, toStatus) {
		switch action.Type {
		case "append_section":
			newBody, changed := appendToSection(issue.BodyRaw, action.Title, action.Body)
			if changed {
				result.Update.Body = &newBody
				issue.BodyRaw = newBody
				result.BodyChanged = true
				result.BodyAppended = true
			}
		case "inject_prompt":
			if strings.TrimSpace(action.Prompt) != "" {
				result.InjectedPrompts = append(result.InjectedPrompts, strings.TrimSpace(action.Prompt))
			}
		case "set_fields":
			switch action.Field {
			case "assignee":
				result.Update.Assignee = stringPtr(action.Value)
			case "approved_for":
				result.Update.ApprovedFor = stringPtr(action.Value)
			case "priority":
				result.Update.Priority = stringPtr(action.Value)
			case "status":
				result.Update.Status = stringPtr(action.Value)
			}
		}
	}

	if tmplBody, appended := w.AppendTemplate(issue.BodyRaw, toStatus); appended {
		result.Update.Body = &tmplBody
		issue.BodyRaw = tmplBody
		result.BodyChanged = true
		result.BodyAppended = true
	}

	if issue.ApprovedFor != "" {
		result.Update.ApprovedFor = stringPtr("")
		result.ClearedApproval = true
	}

	return result
}

// Merge overlays custom workflow config onto this one.
// For matching statuses: appends validation and side_effects (deduped),
// overrides description and template if set in custom.
// New statuses from custom are appended.
func (w *WorkflowConfig) Merge(custom *WorkflowConfig) {
	for _, cs := range custom.Statuses {
		base := w.GetStatus(cs.Name)
		if base == nil {
			w.Statuses = append(w.Statuses, cs)
			continue
		}
		if cs.Description != "" {
			base.Description = cs.Description
		}
		if cs.Template != "" {
			base.Template = cs.Template
		}
		base.Validation = appendUnique(base.Validation, cs.Validation)
		base.SideEffects = appendUnique(base.SideEffects, cs.SideEffects)
	}

	for _, ct := range custom.Transitions {
		base := w.GetTransition(ct.From, ct.To)
		if base == nil {
			w.Transitions = append(w.Transitions, WorkflowTransition{
				From:    ct.From,
				To:      ct.To,
				Actions: append([]WorkflowAction(nil), ct.Actions...),
			})
			continue
		}
		base.Actions = appendUniqueActions(base.Actions, ct.Actions)
	}

	if len(custom.Systems) > 0 {
		if w.Systems == nil {
			w.Systems = make(map[string]WorkflowOverlay, len(custom.Systems))
		}
		for name, overlay := range custom.Systems {
			existing := w.Systems[name]
			cfg := &WorkflowConfig{
				Statuses:    append([]WorkflowStatus(nil), existing.Statuses...),
				Transitions: append([]WorkflowTransition(nil), existing.Transitions...),
			}
			cfg.Merge(&WorkflowConfig{
				Statuses:    overlay.Statuses,
				Transitions: overlay.Transitions,
			})
			w.Systems[name] = WorkflowOverlay{
				Statuses:    cfg.Statuses,
				Transitions: cfg.Transitions,
			}
		}
	}
}

func appendUnique(base, extra []string) []string {
	seen := make(map[string]bool, len(base))
	for _, v := range base {
		seen[v] = true
	}
	for _, v := range extra {
		if !seen[v] {
			base = append(base, v)
			seen[v] = true
		}
	}
	return base
}

func appendUniqueActions(base, extra []WorkflowAction) []WorkflowAction {
	seen := make(map[string]bool, len(base))
	keyFor := func(action WorkflowAction) string {
		return strings.Join([]string{
			action.Type,
			action.Rule,
			action.Status,
			action.Title,
			action.Body,
			action.Prompt,
			action.Field,
			action.Value,
		}, "\x00")
	}
	for _, action := range base {
		seen[keyFor(action)] = true
	}
	for _, action := range extra {
		key := keyFor(action)
		if !seen[key] {
			base = append(base, action)
			seen[key] = true
		}
	}
	return base
}

// NextStatus returns the status name that follows the given one, or empty string.
func (w *WorkflowConfig) NextStatus(current string) string {
	idx := w.GetStatusIndex(current)
	if idx == -1 || idx+1 >= len(w.Statuses) {
		return ""
	}
	return w.Statuses[idx+1].Name
}
