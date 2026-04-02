package main

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"gopkg.in/yaml.v3"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

func canonicalStatusKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

func orderedStatusesForIssue(wf *tracker.WorkflowConfig, current string) []string {
	statuses := append([]string{}, wf.GetStatusOrder()...)
	current = strings.TrimSpace(current)
	if current == "" {
		return statuses
	}
	for _, status := range statuses {
		if status == current {
			return statuses
		}
	}
	return append([]string{current}, statuses...)
}

var funcMap = template.FuncMap{
	"statusColor": func(s string) string {
		colors := map[string]string{
			"idea":          "#8b5cf6",
			"in design":     "#3b82f6",
			"backlog":       "#64748b",
			"in progress":   "#eab308",
			"testing":       "#f97316",
			"human-testing": "#ec4899",
			"documentation": "#14b8a6",
			"done":          "#22c55e",
		}
		if c, ok := colors[canonicalStatusKey(s)]; ok {
			return c
		}
		return "#6b7280"
	},
	"statusTextColor": func(s string) string {
		dark := map[string]bool{"in progress": true, "testing": true, "human-testing": true}
		if dark[canonicalStatusKey(s)] {
			return "#000000"
		}
		return "#ffffff"
	},
	"priorityColor": func(p string) string {
		colors := map[string]string{
			"low":      "#6b7280",
			"medium":   "#3b82f6",
			"high":     "#f97316",
			"critical": "#ef4444",
		}
		if c, ok := colors[p]; ok {
			return c
		}
		return "#6b7280"
	},
	"joinLabels": func(labels []string) string {
		return strings.Join(labels, ", ")
	},
	"safeHTML": func(s string) template.HTML {
		return template.HTML(s)
	},
	"urlEncodeColor": func(s string) string {
		return strings.ReplaceAll(s, "#", "%23")
	},
	"assigneeColor": func(name string) string {
		if name == "" {
			return ""
		}
		colors := []string{
			"#f97316", "#3b82f6", "#22c55e", "#a855f7",
			"#ef4444", "#eab308", "#14b8a6", "#ec4899",
			"#6366f1", "#84cc16",
		}
		h := 0
		for _, c := range name {
			h = h*31 + int(c)
		}
		if h < 0 {
			h = -h
		}
		return colors[h%len(colors)]
	},
	"linkIssueRefs": func(html string, prefix string, slugMap map[string]string) template.HTML {
		// Match #123, #my-slug, #system/my-slug
		re := regexp.MustCompile(`#([a-zA-Z0-9][\w/.-]*)`)
		result := re.ReplaceAllStringFunc(html, func(match string) string {
			ref := match[1:]
			// Direct slug match (e.g. #combat/fix-heat-bug)
			if slug, ok := slugMap[ref]; ok {
				return fmt.Sprintf(`<a href="%s/issue/%s" class="issue-ref">%s</a>`, prefix, slug, match)
			}
			// Try lowercase
			if slug, ok := slugMap[strings.ToLower(ref)]; ok {
				return fmt.Sprintf(`<a href="%s/issue/%s" class="issue-ref">%s</a>`, prefix, slug, match)
			}
			return match
		})
		return template.HTML(result)
	},
}

type Server struct {
	projects []tracker.Project
	tmpl     *template.Template
}

type AgentSession struct {
	Name      string `json:"name"`
	StartTime string `json:"start_time"`
}

type IssueView struct {
	*tracker.Issue
	ActiveSessions []AgentSession
}

func (i *IssueView) HasActiveAgent() bool {
	return i != nil && len(i.ActiveSessions) > 0
}

var listTmuxSessions = defaultListTmuxSessions

func NewServer(projects []tracker.Project) (*Server, error) {
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	return &Server{
		projects: projects,
		tmpl:     tmpl,
	}, nil
}

func (s *Server) findProject(slug string) *tracker.Project {
	for i := range s.projects {
		if s.projects[i].Slug == slug {
			return &s.projects[i]
		}
	}
	return nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleProjectList)
	mux.HandleFunc("/p/", s.handleProjectRoutes)
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	return mux
}

// handleProjectRoutes dispatches /p/<slug>/... to the right handler
func (s *Server) handleProjectRoutes(w http.ResponseWriter, r *http.Request) {
	// Parse /p/<slug>/...
	path := strings.TrimPrefix(r.URL.Path, "/p/")
	parts := strings.SplitN(path, "/", 2)
	projectSlug := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	proj := s.findProject(projectSlug)
	if proj == nil {
		http.NotFound(w, r)
		return
	}

	prefix := "/p/" + projectSlug

	switch {
	case rest == "" || rest == "/":
		s.handleList(w, r, proj, prefix)
	case rest == "board":
		s.handleBoard(w, r, proj, prefix)
	case rest == "workflow-flow":
		s.handleWorkflowFlow(w, r, proj, prefix)
	case rest == "workflow-designer" && r.Method == http.MethodGet:
		s.handleWorkflowDesigner(w, r, proj, prefix)
	case rest == "workflow-designer/data" && r.Method == http.MethodGet:
		s.handleWorkflowDesignerData(w, r, proj)
	case rest == "workflow-designer/preview" && r.Method == http.MethodPost:
		s.handleWorkflowDesignerPreview(w, r, proj)
	case rest == "workflow-designer" && r.Method == http.MethodPost:
		s.handleSaveWorkflowDesigner(w, r, proj)
	case rest == "hash":
		s.handleHash(w, r, proj)
	case rest == "issues.json":
		s.handleIssuesJSON(w, r, proj)
	case rest == "docs":
		s.handleDocs(w, r, proj, prefix)
	case strings.HasPrefix(rest, "docs/"):
		s.handleDocPage(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/comments") && r.Method == http.MethodGet:
		s.handleGetComments(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/comments") && r.Method == http.MethodPost:
		s.handleAddComment(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/comments/toggle") && r.Method == http.MethodPost:
		s.handleToggleComment(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/comments/delete") && r.Method == http.MethodPost:
		s.handleDeleteComment(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/dispatch") && r.Method == http.MethodPost:
		s.handleDispatchAgent(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/edit-in-nvim") && r.Method == http.MethodPost:
		s.handleEditIssueInNvim(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/approve") && r.Method == http.MethodPost:
		s.handleApproveIssue(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/delete") && r.Method == http.MethodPost:
		s.handleDeleteIssue(w, r, proj, prefix)
	case rest == "issues/create" && r.Method == http.MethodPost:
		s.handleCreateIssue(w, r, proj, prefix)
	case rest == "upload" && r.Method == http.MethodPost:
		s.handleUpload(w, r, proj, prefix)
	case strings.HasPrefix(rest, "attachments/"):
		attachDir := filepath.Join(proj.IssueDir, "attachments")
		http.StripPrefix(prefix+"/attachments/", http.FileServer(http.Dir(attachDir))).ServeHTTP(w, r)
	case strings.HasPrefix(rest, "issue/") && r.Method == http.MethodGet:
		s.handleDetail(w, r, proj, prefix)
	case strings.HasPrefix(rest, "issue/") && r.Method == http.MethodPost:
		s.handleUpdateIssue(w, r, proj, prefix)
	default:
		http.NotFound(w, r)
	}
}

// --- Project List ---

type ProjectListData struct {
	Projects []ProjectSummary
}

type ProjectSummary struct {
	Name       string
	Slug       string
	IssueCount int
	DocCount   int
}

func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// If only one project, redirect directly
	if len(s.projects) == 1 {
		http.Redirect(w, r, "/p/"+s.projects[0].Slug+"/", http.StatusFound)
		return
	}

	summaries := make([]ProjectSummary, len(s.projects))
	var wg sync.WaitGroup
	for i, p := range s.projects {
		wg.Add(1)
		go func(i int, p tracker.Project) {
			defer wg.Done()
			issues, _ := tracker.LoadIssues(p.IssueDir)
			docs, _ := tracker.LoadDocs(p.DocsDir)
			summaries[i] = ProjectSummary{
				Name:       p.Name,
				Slug:       p.Slug,
				IssueCount: len(issues),
				DocCount:   len(docs),
			}
		}(i, p)
	}
	wg.Wait()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "projects.html", ProjectListData{Projects: summaries}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- Issue List ---

type ListData struct {
	Issues      []*IssueView
	Statuses    []string
	Systems     []string
	Priorities  []string
	Labels      []string
	Assignees   []string
	Filter      FilterParams
	Total       int
	Filtered    int
	Prefix      string
	ProjectName string
	ActiveBots  int
}

type FilterParams struct {
	Status   string
	System   string
	Priority string
	Label    string
	Assignee string
	Search   string
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	total := len(issues)
	statuses, systems, priorities, labels, assignees := tracker.CollectFilterValues(issues)
	systems = mergeSubdirSystems(systems, proj.IssueDir)

	filter := FilterParams{
		Status:   r.URL.Query().Get("status"),
		System:   r.URL.Query().Get("system"),
		Priority: r.URL.Query().Get("priority"),
		Label:    r.URL.Query().Get("label"),
		Assignee: r.URL.Query().Get("assignee"),
		Search:   r.URL.Query().Get("search"),
	}

	filtered := filterIssues(issues, filter)
	sessionMap, activeBots := sessionsByIssueSlug(issues)

	data := ListData{
		Issues:      issueViews(filtered, sessionMap),
		Statuses:    statuses,
		Systems:     systems,
		Priorities:  priorities,
		Labels:      labels,
		Assignees:   assignees,
		Filter:      filter,
		Total:       total,
		Filtered:    len(filtered),
		Prefix:      prefix,
		ProjectName: proj.Name,
		ActiveBots:  activeBots,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "list.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- Issue Detail ---

type DetailData struct {
	Issue         *IssueView
	BackURL       string
	Prefix        string
	ProjectName   string
	Statuses      []string
	SlugMap       map[string]string
	NeedsApproval string // next status name if it requires human approval, else empty
	ActiveBots    int
}

type BodyEditResponse struct {
	Status  string `json:"status"`
	Session string `json:"session,omitempty"`
	Message string `json:"message,omitempty"`
}

var launchIssueBodyEditor = startIssueBodyEditor

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	path := strings.TrimPrefix(r.URL.Path, prefix+"/issue/")
	slug := path
	if slug == "" {
		http.NotFound(w, r)
		return
	}

	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var found *tracker.Issue
	for _, issue := range issues {
		if issue.Slug == slug {
			found = issue
			break
		}
	}

	if found == nil {
		http.NotFound(w, r)
		return
	}

	backURL := prefix + "/"
	if from := r.URL.Query().Get("from"); from != "" {
		// Only allow known safe relative paths
		if strings.HasPrefix(from, "board") || strings.HasPrefix(from, "docs") {
			backURL = prefix + "/" + from
		}
	}

	// Build ref→slug map for #ref links
	slugMap := map[string]string{}
	for _, issue := range issues {
		// By filename base (e.g. "123" from "123.md") for numeric refs
		fname := strings.TrimSuffix(filepath.Base(issue.FilePath), ".md")
		slugMap[fname] = issue.Slug
		// By slug itself (e.g. "combat/fix-heat-bug") for slug refs
		slugMap[issue.Slug] = issue.Slug
		// By slug without system prefix (e.g. "fix-heat-bug")
		slugMap[filepath.Base(issue.Slug)] = issue.Slug
	}

	sessionMap, activeBots := sessionsByIssueSlug(issues)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wf := proj.LoadWorkflowForIssue(found)
	statuses := orderedStatusesForIssue(wf, found.Status)

	// Check if the next transition requires human approval.
	var needsApproval string
	nextStatus := wf.NextStatus(found.Status)
	if nextStatus != "" {
		if transition := wf.GetTransition(found.Status, nextStatus); transition != nil {
			for _, action := range transition.Actions {
				if action.Type == "require_human_approval" {
					needsApproval = nextStatus
					break
				}
			}
		}
	}

	if err := s.tmpl.ExecuteTemplate(w, "detail.html", DetailData{
		Issue:         issueView(found, sessionMap),
		BackURL:       backURL,
		Prefix:        prefix,
		ProjectName:   proj.Name,
		Statuses:      statuses,
		SlugMap:       slugMap,
		NeedsApproval: needsApproval,
		ActiveBots:    activeBots,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- Board ---

type BoardColumn struct {
	Status      string
	Description string
	Issues      []*IssueView
}

type BoardData struct {
	Columns     []*BoardColumn
	Total       int
	Versions    []string
	Version     string
	Systems     []string
	System      string
	Assignees   []string
	Assignee    string
	Priorities  []string
	Labels      []string
	Prefix      string
	ProjectName string
	ActiveBots  int
}

type WorkflowDesignerData struct {
	Prefix         string
	ProjectName    string
	WorkflowJSON   string
	WorkflowYAML   string
	WorkflowIssues string
	WorkflowSource string
	WorkflowTarget string
}

type WorkflowFlowData struct {
	Prefix      string
	ProjectName string
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	versionSet := map[string]bool{}
	systemSet := map[string]bool{}
	assigneeSet := map[string]bool{}
	prioritySet := map[string]bool{}
	labelSet := map[string]bool{}
	for _, issue := range issues {
		if issue.Version != "" {
			versionSet[issue.Version] = true
		}
		if issue.System != "" {
			systemSet[issue.System] = true
		}
		if issue.Assignee != "" {
			assigneeSet[issue.Assignee] = true
		}
		if issue.Priority != "" {
			prioritySet[issue.Priority] = true
		}
		for _, l := range issue.Labels {
			labelSet[l] = true
		}
	}
	var versions []string
	for v := range versionSet {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	var systems []string
	for s := range systemSet {
		systems = append(systems, s)
	}
	systems = mergeSubdirSystems(systems, proj.IssueDir)
	var assignees []string
	for a := range assigneeSet {
		assignees = append(assignees, a)
	}
	sort.Strings(assignees)
	var priorities []string
	for p := range prioritySet {
		priorities = append(priorities, p)
	}
	sort.Strings(priorities)
	var labels []string
	for l := range labelSet {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	versionFilter := r.URL.Query().Get("version")
	systemFilter := r.URL.Query().Get("system")
	assigneeFilter := r.URL.Query().Get("assignee")

	var filtered []*tracker.Issue
	for _, issue := range issues {
		if versionFilter != "" && issue.Version != versionFilter {
			continue
		}
		if systemFilter != "" && !strings.EqualFold(issue.System, systemFilter) {
			continue
		}
		if assigneeFilter == "_claimed" && issue.Assignee == "" {
			continue
		}
		if assigneeFilter == "_unclaimed" && issue.Assignee != "" {
			continue
		}
		if assigneeFilter != "" && assigneeFilter != "_claimed" && assigneeFilter != "_unclaimed" && issue.Assignee != assigneeFilter {
			continue
		}
		filtered = append(filtered, issue)
	}
	issues = filtered

	wf := proj.LoadWorkflow()
	statusOrder := wf.GetStatusOrder()
	statusDescs := wf.GetStatusDescriptions()
	sessionMap, activeBots := sessionsByIssueSlug(issues)

	byStatus := map[string][]*IssueView{}
	seen := map[string]bool{}
	for _, issue := range issues {
		st := issue.Status
		if st == "" {
			st = "none"
		}
		byStatus[st] = append(byStatus[st], issueView(issue, sessionMap))
		seen[st] = true
	}

	var columns []*BoardColumn
	added := map[string]bool{}
	for _, st := range statusOrder {
		desc := statusDescs[st]
		columns = append(columns, &BoardColumn{Status: st, Description: desc, Issues: byStatus[st]})
		added[st] = true
	}
	for st := range seen {
		if !added[st] {
			columns = append(columns, &BoardColumn{Status: st, Issues: byStatus[st]})
		}
	}

	data := BoardData{
		Columns:     columns,
		Total:       len(issues),
		Versions:    versions,
		Version:     versionFilter,
		Systems:     systems,
		System:      systemFilter,
		Assignees:   assignees,
		Assignee:    assigneeFilter,
		Priorities:  priorities,
		Labels:      labels,
		Prefix:      prefix,
		ProjectName: proj.Name,
		ActiveBots:  activeBots,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "board.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleWorkflowDesigner(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	wf := proj.LoadWorkflow()
	workflowJSON, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	workflowYAML, err := yaml.Marshal(wf)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type workflowIssue struct {
		Slug   string `json:"slug"`
		Title  string `json:"title"`
		Status string `json:"status"`
		System string `json:"system"`
	}
	issueOptions := make([]workflowIssue, 0, len(issues))
	for _, issue := range issues {
		issueOptions = append(issueOptions, workflowIssue{
			Slug:   issue.Slug,
			Title:  issue.Title,
			Status: issue.Status,
			System: issue.System,
		})
	}
	issuesJSON, err := json.Marshal(issueOptions)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	source := "built-in default workflow"
	target := workflowFileTarget(proj)
	switch {
	case proj.WorkflowFile != "":
		source = proj.WorkflowFile
	case fileExists("workflow.yaml"):
		source = "workflow.yaml"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "workflow-designer.html", WorkflowDesignerData{
		Prefix:         prefix,
		ProjectName:    proj.Name,
		WorkflowJSON:   string(workflowJSON),
		WorkflowYAML:   string(workflowYAML),
		WorkflowIssues: string(issuesJSON),
		WorkflowSource: source,
		WorkflowTarget: target,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleWorkflowFlow(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "workflow-flow.html", WorkflowFlowData{
		Prefix:      prefix,
		ProjectName: proj.Name,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleWorkflowDesignerData(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	wf := proj.LoadWorkflow()
	source := "built-in default workflow"
	target := workflowFileTarget(proj)
	switch {
	case proj.WorkflowFile != "":
		source = proj.WorkflowFile
	case fileExists("workflow.yaml"):
		source = "workflow.yaml"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"workflow": wf,
		"source":   source,
		"target":   target,
	})
}

func (s *Server) handleWorkflowDesignerPreview(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	var req struct {
		Slug string `json:"slug"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Slug) == "" || strings.TrimSpace(req.To) == "" {
		http.Error(w, `{"error":"slug and to are required"}`, http.StatusBadRequest)
		return
	}

	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed loading issues: %s"}`, strings.ReplaceAll(err.Error(), `"`, `'`)), http.StatusInternalServerError)
		return
	}

	var issue *tracker.Issue
	for _, candidate := range issues {
		if candidate.Slug == req.Slug {
			issue = candidate
			break
		}
	}
	if issue == nil {
		http.Error(w, `{"error":"issue not found"}`, http.StatusNotFound)
		return
	}

	comments, err := tracker.LoadComments(issue.FilePath)
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, fmt.Sprintf(`{"error":"failed loading comments: %s"}`, strings.ReplaceAll(err.Error(), `"`, `'`)), http.StatusInternalServerError)
		return
	}

	wf := proj.LoadWorkflow().ForSystem(issue.System)
	preview := wf.PreviewTransition(issue, issue.Status, strings.TrimSpace(req.To), issue.System, comments)
	if !wf.IsValidTransition(issue.Status, strings.TrimSpace(req.To)) {
		preview.Allowed = false
		preview.ValidationError = fmt.Sprintf("cannot transition from %q to %q", issue.Status, strings.TrimSpace(req.To))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"issue": map[string]string{
			"slug":   issue.Slug,
			"title":  issue.Title,
			"status": issue.Status,
			"system": issue.System,
		},
		"preview": preview,
	})
}

func (s *Server) handleSaveWorkflowDesigner(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	var body struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(body.YAML) == "" {
		http.Error(w, `{"error":"yaml is required"}`, http.StatusBadRequest)
		return
	}

	var cfg tracker.WorkflowConfig
	if err := yaml.Unmarshal([]byte(body.YAML), &cfg); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid workflow yaml: %s"}`, strings.ReplaceAll(err.Error(), `"`, `'`)), http.StatusBadRequest)
		return
	}

	target := workflowFileTarget(proj)
	if err := os.WriteFile(target, []byte(body.YAML+"\n"), 0644); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"save failed: %s"}`, strings.ReplaceAll(err.Error(), `"`, `'`)), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"path":   target,
	})
}

func workflowFileTarget(proj *tracker.Project) string {
	if proj.WorkflowFile != "" {
		return proj.WorkflowFile
	}
	return "workflow.yaml"
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// --- Docs ---

type DocsData struct {
	Page        *tracker.DocPage
	Pages       []*tracker.DocPage
	Sections    []tracker.DocSection
	Prefix      string
	ProjectName string
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	pages, err := tracker.LoadDocs(proj.DocsDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(pages) > 0 {
		http.Redirect(w, r, fmt.Sprintf("%s/docs/%s", prefix, pages[0].Slug), http.StatusFound)
		return
	}

	sections := tracker.GroupDocSections(pages)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "docs.html", DocsData{Pages: pages, Sections: sections, Prefix: prefix, ProjectName: proj.Name}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleDocPage(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := strings.TrimPrefix(r.URL.Path, prefix+"/docs/")
	if slug == "" {
		s.handleDocs(w, r, proj, prefix)
		return
	}

	pages, err := tracker.LoadDocs(proj.DocsDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var found *tracker.DocPage
	for _, p := range pages {
		if p.Slug == slug {
			found = p
			break
		}
	}

	if found == nil {
		http.NotFound(w, r)
		return
	}

	sections := tracker.GroupDocSections(pages)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "docs.html", DocsData{Page: found, Pages: pages, Sections: sections, Prefix: prefix, ProjectName: proj.Name}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- Update Issue ---

func (s *Server) handleUpdateIssue(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := strings.TrimPrefix(r.URL.Path, prefix+"/issue/")
	if slug == "" {
		http.NotFound(w, r)
		return
	}

	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var found *tracker.Issue
	for _, issue := range issues {
		if issue.Slug == slug {
			found = issue
			break
		}
	}

	if found == nil {
		http.NotFound(w, r)
		return
	}

	var update tracker.IssueUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := tracker.UpdateIssueFrontmatter(found.FilePath, update); err != nil {
		http.Error(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// --- Comments ---

func (s *Server) findIssueBySlug(proj *tracker.Project, slug string) *tracker.Issue {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		return nil
	}
	for _, issue := range issues {
		if issue.Slug == slug {
			return issue
		}
	}
	return nil
}

func (s *Server) handleGetComments(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := s.extractCommentSlug(r.URL.Path, prefix, "/comments")

	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}

	comments, err := tracker.LoadComments(issue.FilePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if comments == nil {
		comments = []tracker.Comment{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(comments)
}

type AddCommentRequest struct {
	Block int    `json:"block"`
	Text  string `json:"text"`
}

func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := s.extractCommentSlug(r.URL.Path, prefix, "/comments")

	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}

	var req AddCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}

	if err := tracker.AddComment(issue.FilePath, req.Block, req.Text, "app"); err != nil {
		http.Error(w, "failed to save: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

type CommentIDRequest struct {
	ID int `json:"id"`
}

func (s *Server) extractCommentSlug(path, prefix, suffix string) string {
	slug := strings.TrimPrefix(path, prefix+"/issue/")
	slug = strings.TrimSuffix(slug, suffix)
	return slug
}

func (s *Server) handleToggleComment(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := s.extractCommentSlug(r.URL.Path, prefix, "/comments/toggle")

	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}

	var req CommentIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := tracker.ToggleComment(issue.FilePath, req.ID); err != nil {
		http.Error(w, "failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := s.extractCommentSlug(r.URL.Path, prefix, "/comments/delete")

	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}

	var req CommentIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := tracker.DeleteComment(issue.FilePath, req.ID); err != nil {
		http.Error(w, "failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// --- Content Hash (for auto-refresh) ---

func (s *Server) handleHash(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	sessionMap, activeBots := sessionsByIssueSlug(issues)
	h := sha256.New()
	for _, issue := range issues {
		fmt.Fprintf(h, "%s:%s:%s:%d\n", issue.Slug, issue.Status, issue.Assignee, issue.ModTime.UnixNano())
		for _, session := range sessionMap[issue.Slug] {
			fmt.Fprintf(h, "session:%s:%s:%s\n", issue.Slug, session.Name, session.StartTime)
		}
	}
	fmt.Fprintf(h, "active-bots:%d\n", activeBots)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"hash": fmt.Sprintf("%x", h.Sum(nil))})
}

type issueJSON struct {
	Slug           string         `json:"slug"`
	Title          string         `json:"title"`
	Status         string         `json:"status"`
	System         string         `json:"system"`
	Priority       string         `json:"priority"`
	Assignee       string         `json:"assignee"`
	Version        string         `json:"version"`
	Labels         []string       `json:"labels"`
	ActiveSessions []AgentSession `json:"active_sessions"`
	HasActiveAgent bool           `json:"has_active_agent"`
}

func (s *Server) handleIssuesJSON(w http.ResponseWriter, r *http.Request, proj *tracker.Project) {
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	sessionMap, _ := sessionsByIssueSlug(issues)
	result := make([]issueJSON, len(issues))
	for i, issue := range issues {
		activeSessions := append([]AgentSession(nil), sessionMap[issue.Slug]...)
		result[i] = issueJSON{
			Slug:           issue.Slug,
			Title:          issue.Title,
			Status:         issue.Status,
			System:         issue.System,
			Priority:       issue.Priority,
			Assignee:       issue.Assignee,
			Version:        issue.Version,
			Labels:         issue.Labels,
			ActiveSessions: activeSessions,
			HasActiveAgent: len(activeSessions) > 0,
		}
		if result[i].Labels == nil {
			result[i].Labels = []string{}
		}
		if result[i].ActiveSessions == nil {
			result[i].ActiveSessions = []AgentSession{}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// --- Delete Issue ---

func (s *Server) handleDeleteIssue(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := strings.TrimPrefix(r.URL.Path, prefix+"/issue/")
	slug = strings.TrimSuffix(slug, "/delete")

	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}

	if err := tracker.DeleteIssue(issue.FilePath); err != nil {
		http.Error(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleApproveIssue(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := strings.TrimPrefix(r.URL.Path, prefix+"/issue/")
	slug = strings.TrimSuffix(slug, "/approve")

	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Status == "" {
		http.Error(w, `{"error":"status required"}`, http.StatusBadRequest)
		return
	}

	// Toggle: if already human-approved for this status, clear it
	var humanApproval string
	if !strings.EqualFold(issue.HumanApproval, body.Status) {
		humanApproval = body.Status
	}

	update := tracker.IssueUpdate{HumanApproval: &humanApproval}
	if err := tracker.UpdateIssueFrontmatter(issue.FilePath, update); err != nil {
		http.Error(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "human_approval": humanApproval})
}

func resolveProjectWorkDir(proj *tracker.Project) string {
	if proj != nil && proj.WorkDir != "" {
		return proj.WorkDir
	}
	workDir, _ := os.Getwd()
	return workDir
}

func startIssueBodyEditor(proj *tracker.Project, issue *tracker.Issue) (BodyEditResponse, error) {
	if issue == nil {
		return BodyEditResponse{}, fmt.Errorf("issue is required")
	}

	workDir := resolveProjectWorkDir(proj)
	session := tmuxSessionName(issue.Slug) + "-edit"
	waitSignal := session + "-done"
	baseContent, err := os.ReadFile(issue.FilePath)
	if err != nil {
		return BodyEditResponse{}, fmt.Errorf("read issue file: %w", err)
	}
	baseHash := sha256.Sum256(baseContent)

	bodyFile, err := os.CreateTemp("", "issue-body-*.md")
	if err != nil {
		return BodyEditResponse{}, fmt.Errorf("create temp body file: %w", err)
	}
	bodyPath := bodyFile.Name()
	bodyContent := issue.BodyRaw
	if bodyContent != "" && !strings.HasSuffix(bodyContent, "\n") {
		bodyContent += "\n"
	}
	if _, err := bodyFile.WriteString(bodyContent); err != nil {
		bodyFile.Close()
		os.Remove(bodyPath)
		return BodyEditResponse{}, fmt.Errorf("write temp body file: %w", err)
	}
	if err := bodyFile.Close(); err != nil {
		os.Remove(bodyPath)
		return BodyEditResponse{}, fmt.Errorf("close temp body file: %w", err)
	}

	statusFile, err := os.CreateTemp("", "issue-body-edit-status-*.txt")
	if err != nil {
		os.Remove(bodyPath)
		return BodyEditResponse{}, fmt.Errorf("create temp status file: %w", err)
	}
	statusPath := statusFile.Name()
	statusFile.Close()

	cleanup := func() {
		os.Remove(bodyPath)
		os.Remove(statusPath)
	}

	if err := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", workDir).Run(); err != nil {
		cleanup()
		return BodyEditResponse{}, fmt.Errorf("create tmux session: %w", err)
	}

	sessionReady := false
	defer func() {
		if !sessionReady {
			exec.Command("tmux", "kill-session", "-t", session).Run()
			cleanup()
		}
	}()

	exec.Command("tmux", "rename-window", "-t", session, issue.Slug).Run()

	if proj != nil && proj.I3Workspace != "" {
		if err := exec.Command("i3-msg", "workspace", proj.I3Workspace).Run(); err != nil {
			return BodyEditResponse{}, fmt.Errorf("switch i3 workspace: %w", err)
		}
	}

	if err := exec.Command("i3-msg", "exec", fmt.Sprintf("alacritty -e tmux attach -t %s", session)).Run(); err != nil {
		return BodyEditResponse{}, fmt.Errorf("open alacritty: %w", err)
	}

	time.Sleep(500 * time.Millisecond)

	editCmd := fmt.Sprintf("nvim %q; code=$?; printf '%%s' \"$code\" > %q; tmux wait-for -S %q; exit $code", bodyPath, statusPath, waitSignal)
	if err := exec.Command("tmux", "send-keys", "-t", session, editCmd, "Enter").Run(); err != nil {
		return BodyEditResponse{}, fmt.Errorf("start nvim: %w", err)
	}

	issueFilePath := issue.FilePath
	go func() {
		defer cleanup()
		if err := exec.Command("tmux", "wait-for", waitSignal).Run(); err != nil {
			return
		}
		defer exec.Command("tmux", "kill-session", "-t", session).Run()

		statusBytes, err := os.ReadFile(statusPath)
		if err != nil {
			return
		}
		if strings.TrimSpace(string(statusBytes)) != "0" {
			return
		}

		updatedBody, err := os.ReadFile(bodyPath)
		if err != nil {
			return
		}
		updated := strings.TrimRight(string(updatedBody), "\n")
		currentContent, err := os.ReadFile(issueFilePath)
		if err != nil {
			return
		}
		if sha256.Sum256(currentContent) != baseHash {
			return
		}
		_ = tracker.UpdateIssueFrontmatter(issueFilePath, tracker.IssueUpdate{Body: &updated})
	}()

	sessionReady = true
	return BodyEditResponse{
		Status:  "launched",
		Session: session,
		Message: "Opened in nvim. The issue body will sync back after the editor exits.",
	}, nil
}

func (s *Server) handleEditIssueInNvim(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := strings.TrimPrefix(r.URL.Path, prefix+"/issue/")
	slug = strings.TrimSuffix(slug, "/edit-in-nvim")

	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}

	resp, err := launchIssueBodyEditor(proj, issue)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- Create Issue ---

type CreateIssueRequest struct {
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	System   string   `json:"system"`
	Version  string   `json:"version"`
	Priority string   `json:"priority"`
	Labels   []string `json:"labels"`
	Body     string   `json:"body"`
}

func (s *Server) handleCreateIssue(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	var req CreateIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	_, slug, err := tracker.CreateIssueFileOpts(proj.IssueDir, tracker.CreateIssueOpts{
		Title:    req.Title,
		Status:   req.Status,
		System:   req.System,
		Version:  req.Version,
		Priority: req.Priority,
		Labels:   req.Labels,
		Body:     req.Body,
	})
	if err != nil {
		http.Error(w, "create failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "slug": slug})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "file too large (max 10MB)", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}

	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		http.Error(w, "only image files allowed", http.StatusBadRequest)
		return
	}

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		switch contentType {
		case "image/png":
			ext = ".png"
		case "image/jpeg":
			ext = ".jpg"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		default:
			ext = ".png"
		}
	}

	hash := sha256.Sum256(data)
	filename := fmt.Sprintf("%x%s", hash[:16], ext)

	attachDir := filepath.Join(proj.IssueDir, "attachments")
	os.MkdirAll(attachDir, 0755)

	dest := filepath.Join(attachDir, filename)
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		if err := os.WriteFile(dest, data, 0644); err != nil {
			http.Error(w, "write error", http.StatusInternalServerError)
			return
		}
	}

	urlPath := prefix + "/attachments/" + filename
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": urlPath})
}

// --- Dispatch to Agent ---

func buildAgentPrompt(issue *tracker.Issue, wf *tracker.WorkflowConfig) string {
	currentPrompt := "Use issue-cli to inspect the current workflow requirements for this status before making changes."
	if wf != nil {
		if prompt := wf.StatusPrompt(issue.Status); strings.TrimSpace(prompt) != "" {
			currentPrompt = prompt
		}
	}

	statusReminder := ""
	switch issue.Status {
	case "in design":
		statusReminder = "When the design is complete, stop and ask the human to approve backlog in the issue viewer before attempting that transition."
	case "backlog":
		statusReminder = "Do not run `issue-cli start` until the issue is human-approved for `in progress` in the issue viewer."
	}

	return fmt.Sprintf(`You have been assigned this issue: %s

## Before you start

Learn the workflow process first:
  issue-cli process workflow      # understand the status lifecycle
  issue-cli process transitions   # understand what each transition requires

## Your goal

Move this issue forward correctly using the configured workflow.
Use issue-cli to inspect the current status requirements, complete the required work, and only transition when the workflow says the issue is ready.
Stop and ask the user whenever clarification, approval, or manual verification is required.

## Current status guidance

%s

%s

## How to work on this issue

1. Run: issue-cli start %s
   If the issue is already approved for the next status, this claims the issue and shows your checklist and next steps.
   If approval is missing, stop and ask the human to approve it in the issue viewer.

2. Run: issue-cli show %s
   Read the full context — body, comments, checklist status.

3. Work through each checkbox in the issue one at a time. After completing each one, mark it:
   issue-cli check %s "<checkbox text>"

4. If you are unsure about something or need clarification, ask the user before proceeding.

5. When the current status checkboxes are done, transition to the next status:
   issue-cli transition %s --to "<next-status>"
   The CLI will tell you what the valid next status is and what it requires.

6. Repeat steps 3-5 for each status. Each transition may add new checkboxes — work through them all.

## issue-cli commands you can use freely

These are safe to run without asking the user:
  issue-cli process workflow          # learn status lifecycle
  issue-cli process transitions       # learn transition requirements
  issue-cli show %s                   # full context dump
  issue-cli checklist %s              # checkbox status
  issue-cli next                      # see available work
  issue-cli start %s                  # claim and begin work
  issue-cli check %s "<text>"         # mark a checkbox done
  issue-cli transition %s --to "<next-status>"  # move forward
  issue-cli append %s --body "content"          # append section to issue body
  issue-cli retrospective %s --body "content"   # save workflow feedback under retros/ in the project

## CRITICAL: NEVER modify issue .md files manually. Always use issue-cli commands.

## If you encounter a bug in issue-cli itself, report it:
  issue-cli report-bug "description of what went wrong"
This saves a bug report under bugs/ in the server root.
The current issue slug is attached automatically for dispatched sessions.

## Workflow review

If you stop because human approval, manual verification, clarification, or tooling/workflow blockage is required, run:
  issue-cli retrospective %s --body "Base workflow: ...\nSubsystem workflow for %s: ...\nMissing system-specific instructions: ...\nTooling/workflow friction: ..."
This saves a retrospective under retros/ in the project.

Then briefly tell the user how the workflow could be improved.
Cover both:
  - the base workflow
  - the relevant subsystem workflow guidance for this issue
  - any missing system-specific instructions that should be added for %s

If you hit friction, ambiguity, or missing guardrails while using issue-cli or the workflow, call that out explicitly.

## Commands that require user approval — DO NOT run without asking

  issue-cli transition <slug> --to "done"       # only humans close issues
  Any transition backwards                       # ask first
  Creating or deleting issues                    # ask first

## Issue metadata
  Title: %s
  Status: %s
  Priority: %s

%s`,
		issue.Slug,
		currentPrompt,
		statusReminder,
		issue.Slug,
		issue.Slug,
		issue.Slug,
		issue.Slug,
		issue.Slug, issue.Slug, issue.Slug, issue.Slug, issue.Slug, issue.Slug, issue.Slug, issue.System, issue.System, issue.Slug,
		issue.Title, issue.Status, issue.Priority,
		issue.BodyRaw)
}

type DispatchStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type DispatchResponse struct {
	Status  string         `json:"status"`
	Prompt  string         `json:"prompt"`
	Session string         `json:"session"`
	LogFile string         `json:"log_file,omitempty"`
	Steps   []DispatchStep `json:"steps"`
}

func tmuxSessionName(slug string) string {
	r := strings.NewReplacer("/", "-", ".", "-", " ", "-")
	return "agent-" + r.Replace(slug)
}

func runStep(steps *[]DispatchStep, name string, cmd *exec.Cmd) bool {
	out, err := cmd.CombinedOutput()
	if err != nil {
		*steps = append(*steps, DispatchStep{Name: name, Status: "error", Detail: strings.TrimSpace(string(out))})
		return false
	}
	*steps = append(*steps, DispatchStep{Name: name, Status: "ok"})
	return true
}

func (s *Server) handleDispatchAgent(w http.ResponseWriter, r *http.Request, proj *tracker.Project, prefix string) {
	slug := strings.TrimPrefix(r.URL.Path, prefix+"/issue/")
	slug = strings.TrimSuffix(slug, "/dispatch")

	issue := s.findIssueBySlug(proj, slug)
	if issue == nil {
		http.NotFound(w, r)
		return
	}

	// Parse agent type from request body (default: claude)
	agentType := "claude"
	var body struct {
		Agent string `json:"agent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Agent != "" {
		agentType = body.Agent
	}

	wf := proj.LoadWorkflowForIssue(issue)
	prompt := buildAgentPrompt(issue, wf)
	session := tmuxSessionName(slug)

	workDir := resolveProjectWorkDir(proj)

	// Write prompt to temp file (needed because tmux send-keys can't handle multi-line reliably)
	promptFile, err := os.CreateTemp("", "agent-prompt-*.txt")
	if err != nil {
		http.Error(w, "failed to create temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	promptFile.WriteString(prompt)
	promptFile.Close()

	steps := []DispatchStep{}
	sessionLogDir := filepath.Join(workDir, ".agent-logs", session)
	rawLog := filepath.Join(sessionLogDir, "rawlog")
	cliLog := filepath.Join(sessionLogDir, session+".clilog")
	respond := func(status string) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(DispatchResponse{Status: status, Prompt: prompt, Session: session, LogFile: rawLog, Steps: steps})
	}

	// Step 1: Create tmux session (detached) in project dir
	if !runStep(&steps, fmt.Sprintf("Create tmux session in %s", workDir),
		exec.Command("tmux", "new-session", "-d", "-s", session, "-c", workDir)) {
		respond("error")
		return
	}

	// Step 2: Name the tmux window with the issue slug
	exec.Command("tmux", "rename-window", "-t", session, slug).Run()

	// Step 3: Create log directory and pipe session output to rawlog
	os.MkdirAll(sessionLogDir, 0755)
	runStep(&steps, fmt.Sprintf("Log to %s", rawLog),
		exec.Command("tmux", "pipe-pane", "-t", session, "-o", fmt.Sprintf("cat >> %s", rawLog)))

	// Step 4: Set ISSUE_CLI_LOG env var so issue-cli writes to clilog
	runStep(&steps, fmt.Sprintf("CLI log to %s", cliLog),
		exec.Command("tmux", "send-keys", "-t", session, fmt.Sprintf("export ISSUE_CLI_LOG=%q", cliLog), "Enter"))

	serverRoot, _ := os.Getwd()
	runStep(&steps, fmt.Sprintf("Server root env %s", serverRoot),
		exec.Command("tmux", "send-keys", "-t", session, fmt.Sprintf("export ISSUE_VIEWER_SERVER_PWD=%q", serverRoot), "Enter"))
	runStep(&steps, fmt.Sprintf("Issue slug env %s", issue.Slug),
		exec.Command("tmux", "send-keys", "-t", session, fmt.Sprintf("export ISSUE_VIEWER_ISSUE_SLUG=%q", issue.Slug), "Enter"))

	// Step 5: Send explicit cd (belt and suspenders)
	runStep(&steps, fmt.Sprintf("cd %s", workDir),
		exec.Command("tmux", "send-keys", "-t", session, fmt.Sprintf("cd %q", workDir), "Enter"))

	// Step 3: Switch i3 workspace
	if ws := proj.I3Workspace; ws != "" {
		runStep(&steps, fmt.Sprintf("Switch to workspace %s", ws),
			exec.Command("i3-msg", "workspace", ws))
	}

	// Step 4: Open alacritty attached to tmux session
	if !runStep(&steps, "Open alacritty",
		exec.Command("i3-msg", "exec", fmt.Sprintf("alacritty -e tmux attach -t %s", session))) {
		respond("error")
		return
	}

	// Step 5: Wait for shell to be ready in tmux
	time.Sleep(500 * time.Millisecond)

	// Step 6: Launch agent with prompt
	if agentType == "codex" {
		// Codex accepts prompt as CLI argument — pass via file to avoid send-keys/paste issues
		cmd := fmt.Sprintf("codex \"$(cat %s)\"", promptFile.Name())
		runStep(&steps, "Start codex with prompt",
			exec.Command("tmux", "send-keys", "-t", session, cmd, "Enter"))
	} else {
		// Claude: start interactive, then paste prompt
		runStep(&steps, fmt.Sprintf("Start %s (interactive)", agentType),
			exec.Command("tmux", "send-keys", "-t", session, agentType, "Enter"))

		time.Sleep(3 * time.Second)

		runStep(&steps, "Load prompt into tmux buffer",
			exec.Command("tmux", "load-buffer", promptFile.Name()))

		runStep(&steps, fmt.Sprintf("Paste prompt to %s", agentType),
			exec.Command("tmux", "paste-buffer", "-t", session))

		time.Sleep(200 * time.Millisecond)
		runStep(&steps, "Submit prompt",
			exec.Command("tmux", "send-keys", "-t", session, "Enter"))
	}

	respond("dispatched")
}

// mergeSubdirSystems merges subdirectory names into the systems list so that
// empty folders still appear as available systems.
func mergeSubdirSystems(systems []string, issueDir string) []string {
	subdirSystems := tracker.CollectSubdirSystems(issueDir)
	seen := map[string]bool{}
	for _, s := range systems {
		seen[s] = true
	}
	for _, s := range subdirSystems {
		if !seen[s] {
			systems = append(systems, s)
			seen[s] = true
		}
	}
	sort.Strings(systems)
	return systems
}

// --- Filters ---

func filterIssues(issues []*tracker.Issue, f FilterParams) []*tracker.Issue {
	var result []*tracker.Issue
	for _, issue := range issues {
		if f.Status != "" && issue.Status != f.Status {
			continue
		}
		if f.System != "" && !strings.EqualFold(issue.System, f.System) {
			continue
		}
		if f.Priority != "" && issue.Priority != f.Priority {
			continue
		}
		if f.Label != "" {
			hasLabel := false
			for _, l := range issue.Labels {
				if strings.EqualFold(l, f.Label) {
					hasLabel = true
					break
				}
			}
			if !hasLabel {
				continue
			}
		}
		if f.Assignee == "_claimed" && issue.Assignee == "" {
			continue
		}
		if f.Assignee == "_unclaimed" && issue.Assignee != "" {
			continue
		}
		if f.Assignee != "" && f.Assignee != "_claimed" && f.Assignee != "_unclaimed" && issue.Assignee != f.Assignee {
			continue
		}
		if f.Search != "" {
			search := strings.ToLower(f.Search)
			if !strings.Contains(strings.ToLower(issue.Title), search) &&
				!strings.Contains(strings.ToLower(issue.BodyRaw), search) {
				continue
			}
		}
		result = append(result, issue)
	}
	return result
}

func issueViews(issues []*tracker.Issue, sessionMap map[string][]AgentSession) []*IssueView {
	result := make([]*IssueView, 0, len(issues))
	for _, issue := range issues {
		result = append(result, issueView(issue, sessionMap))
	}
	return result
}

func issueView(issue *tracker.Issue, sessionMap map[string][]AgentSession) *IssueView {
	if issue == nil {
		return nil
	}
	return &IssueView{
		Issue:          issue,
		ActiveSessions: append([]AgentSession(nil), sessionMap[issue.Slug]...),
	}
}

func sessionsByIssueSlug(issues []*tracker.Issue) (map[string][]AgentSession, int) {
	sessions := listTmuxSessions()
	result := make(map[string][]AgentSession, len(issues))
	matchedSessionNames := map[string]bool{}
	for _, issue := range issues {
		for _, session := range sessions {
			if sessionMatchesIssue(session.Name, issue.Slug) {
				result[issue.Slug] = append(result[issue.Slug], session)
				matchedSessionNames[session.Name] = true
			}
		}
	}
	return result, len(matchedSessionNames)
}

func sessionMatchesIssue(sessionName string, slug string) bool {
	sessionName = strings.ToLower(strings.TrimSpace(sessionName))
	slug = strings.ToLower(strings.TrimSpace(slug))
	if sessionName == "" || slug == "" {
		return false
	}

	candidates := []string{
		slug,
		strings.ReplaceAll(slug, "/", "-"),
		tmuxSessionName(slug),
	}
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(sessionName, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func defaultListTmuxSessions() []AgentSession {
	out, err := exec.Command("tmux", "ls").Output()
	if err != nil {
		return nil
	}

	lineRE := regexp.MustCompile(`^([^:]+):.*\(created ([^)]+)\)`)
	var sessions []AgentSession
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		matches := lineRE.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}

		session := AgentSession{Name: strings.TrimSpace(matches[1])}
		if created, err := time.Parse("Mon Jan _2 15:04:05 2006", strings.TrimSpace(matches[2])); err == nil {
			session.StartTime = created.Format("2006-01-02 15:04:05")
		} else {
			session.StartTime = strings.TrimSpace(matches[2])
		}
		sessions = append(sessions, session)
	}
	return sessions
}
