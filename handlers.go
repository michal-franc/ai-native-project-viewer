package main

import (
	"embed"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
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
	case rest == "hash":
		s.handleHash(w, r, proj)
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
	case strings.HasPrefix(rest, "issue/") && strings.HasSuffix(rest, "/delete") && r.Method == http.MethodPost:
		s.handleDeleteIssue(w, r, proj, prefix)
	case rest == "issues/create" && r.Method == http.MethodPost:
		s.handleCreateIssue(w, r, proj, prefix)
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

	var summaries []ProjectSummary
	for _, p := range s.projects {
		issues, _ := tracker.LoadIssues(p.IssueDir)
		docs, _ := tracker.LoadDocs(p.DocsDir)
		summaries = append(summaries, ProjectSummary{
			Name:       p.Name,
			Slug:       p.Slug,
			IssueCount: len(issues),
			DocCount:   len(docs),
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "projects.html", ProjectListData{Projects: summaries}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- Issue List ---

type ListData struct {
	Issues      []*tracker.Issue
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

	filter := FilterParams{
		Status:   r.URL.Query().Get("status"),
		System:   r.URL.Query().Get("system"),
		Priority: r.URL.Query().Get("priority"),
		Label:    r.URL.Query().Get("label"),
		Assignee: r.URL.Query().Get("assignee"),
		Search:   r.URL.Query().Get("search"),
	}

	filtered := filterIssues(issues, filter)

	data := ListData{
		Issues:      filtered,
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
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "list.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- Issue Detail ---

type DetailData struct {
	Issue       *tracker.Issue
	BackURL     string
	Prefix      string
	ProjectName string
	Statuses    []string
	SlugMap     map[string]string
}

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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	wf := proj.LoadWorkflow()
	statuses := orderedStatusesForIssue(wf, found.Status)
	if err := s.tmpl.ExecuteTemplate(w, "detail.html", DetailData{Issue: found, BackURL: backURL, Prefix: prefix, ProjectName: proj.Name, Statuses: statuses, SlugMap: slugMap}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- Board ---

type BoardColumn struct {
	Status      string
	Description string
	Issues      []*tracker.Issue
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
	sort.Strings(systems)
	var assignees []string
	for a := range assigneeSet {
		assignees = append(assignees, a)
	}
	sort.Strings(assignees)

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

	byStatus := map[string][]*tracker.Issue{}
	seen := map[string]bool{}
	for _, issue := range issues {
		st := issue.Status
		if st == "" {
			st = "none"
		}
		byStatus[st] = append(byStatus[st], issue)
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
		Prefix:      prefix,
		ProjectName: proj.Name,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "board.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
	h := sha256.New()
	for _, issue := range issues {
		fmt.Fprintf(h, "%s:%s:%s:%d\n", issue.Slug, issue.Status, issue.Assignee, issue.ModTime.UnixNano())
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"hash": fmt.Sprintf("%x", h.Sum(nil))})
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

// --- Create Issue ---

type CreateIssueRequest struct {
	Title  string `json:"title"`
	Status string `json:"status"`
	System string `json:"system"`
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

	_, slug, err := tracker.CreateIssueFile(proj.IssueDir, req.Title, req.Status, req.System)
	if err != nil {
		http.Error(w, "create failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "slug": slug})
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
