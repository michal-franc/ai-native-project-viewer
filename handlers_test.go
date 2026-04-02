package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michal-franc/issue-viewer/internal/tracker"
)

// helper: create a temp directory with test issue files and return a Project
func setupTestProject(t *testing.T) (tracker.Project, string) {
	t.Helper()
	tmpDir := t.TempDir()
	issueDir := filepath.Join(tmpDir, "issues")
	docsDir := filepath.Join(tmpDir, "docs")
	os.MkdirAll(issueDir, 0755)
	os.MkdirAll(docsDir, 0755)

	// Create test issues
	issues := map[string]string{
		"bug-in-login.md": `---
title: "Bug in login"
status: "in progress"
system: "Auth"
version: "1.0"
labels:
  - bug
  - urgent
priority: "high"
assignee: "alice"
created: "2025-01-15"
---

Login page crashes on submit.
`,
		"add-dark-mode.md": `---
title: "Add dark mode"
status: "backlog"
system: "UI"
version: "2.0"
labels:
  - enhancement
priority: "medium"
assignee: "bob"
created: "2025-01-10"
---

We need dark mode support.
`,
		"fix-typo.md": `---
title: "Fix typo"
status: "done"
system: "Docs"
priority: "low"
created: "2025-01-05"
---

Fix typo in readme.
`,
	}

	for name, content := range issues {
		if err := os.WriteFile(filepath.Join(issueDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Create test doc
	doc := `---
title: "Getting Started"
order: 1
---

Welcome to the project.
`
	if err := os.WriteFile(filepath.Join(docsDir, "getting-started.md"), []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}

	proj := tracker.Project{
		Name:     "Test Project",
		Slug:     "test-project",
		IssueDir: issueDir,
		DocsDir:  docsDir,
	}
	return proj, tmpDir
}

func newTestServer(t *testing.T, projects []tracker.Project) *httptest.Server {
	t.Helper()
	srv, err := NewServer(projects)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(srv.Routes())
}

func withMockTmuxSessions(t *testing.T, sessions []AgentSession) {
	t.Helper()
	original := listTmuxSessions
	listTmuxSessions = func() []AgentSession { return sessions }
	t.Cleanup(func() {
		listTmuxSessions = original
	})
}

// --- handleProjectList ---

func TestHandleProjectList_RedirectsSingleProject(t *testing.T) {
	proj, _ := setupTestProject(t)
	srv, err := NewServer([]tracker.Project{proj})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/p/test-project/" {
		t.Fatalf("expected redirect to /p/test-project/, got %s", loc)
	}
}

func TestHandleProjectList_ListsMultipleProjects(t *testing.T) {
	proj1, _ := setupTestProject(t)
	proj2 := tracker.Project{
		Name:     "Second Project",
		Slug:     "second-project",
		IssueDir: proj1.IssueDir, // reuse dir
		DocsDir:  proj1.DocsDir,
	}

	ts := newTestServer(t, []tracker.Project{proj1, proj2})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html, got %s", ct)
	}
}

func TestHandleProjectList_404ForNonRootPath(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj, proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// --- handleList ---

func TestHandleList_ReturnsIssueList(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content-type, got %s", ct)
	}
}

func TestHandleList_FiltersWork(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	tests := []struct {
		name  string
		query string
	}{
		{"filter by status", "?status=done"},
		{"filter by system", "?system=Auth"},
		{"filter by priority", "?priority=high"},
		{"filter by label", "?label=bug"},
		{"filter by assignee", "?assignee=alice"},
		{"filter by search", "?search=login"},
		{"combined filters", "?status=in+progress&system=Auth&priority=high"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(ts.URL + "/p/test-project/" + tt.query)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}
		})
	}
}

func TestHandleList_ShowsActiveBotSummaryAndIssueChip(t *testing.T) {
	proj, _ := setupTestProject(t)
	withMockTmuxSessions(t, []AgentSession{
		{Name: "agent-bug-in-login", StartTime: "2026-04-02 21:57:59"},
		{Name: "agent-unrelated-work", StartTime: "2026-04-02 21:58:59"},
	})

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{"1 active bot", "1 agent active"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected list view to contain %q\n%s", want, html)
		}
	}
}

// --- handleBoard ---

func TestHandleBoard_ReturnsBoardView(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/board")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html, got %s", ct)
	}
}

func TestHandleBoard_Filters(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	tests := []struct {
		name  string
		query string
	}{
		{"version filter", "?version=1.0"},
		{"system filter", "?system=Auth"},
		{"assignee filter", "?assignee=alice"},
		{"claimed filter", "?assignee=_claimed"},
		{"unclaimed filter", "?assignee=_unclaimed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(ts.URL + "/p/test-project/board" + tt.query)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}
		})
	}
}

func TestHandleBoard_ShowsActiveBotSummaryAndIssueChip(t *testing.T) {
	proj, _ := setupTestProject(t)
	withMockTmuxSessions(t, []AgentSession{
		{Name: "agent-bug-in-login", StartTime: "2026-04-02 21:57:59"},
		{Name: "agent-unrelated-work", StartTime: "2026-04-02 21:58:59"},
	})

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/board")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{"1 active bot", "1 agent active"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected board view to contain %q\n%s", want, html)
		}
	}
}

func TestBuildAgentPrompt_IncludesCurrentStatusGuidanceAndRetrospectiveTrigger(t *testing.T) {
	issue := &tracker.Issue{
		Slug:     "combat/fix-heat",
		Title:    "Fix heat",
		Status:   "testing",
		System:   "Combat",
		Priority: "high",
		BodyRaw:  "Body text",
	}
	wf := &tracker.WorkflowConfig{
		Statuses: []tracker.WorkflowStatus{
			{Name: "testing", Prompt: "Build relevant automated coverage before handoff."},
		},
	}

	prompt := buildAgentPrompt(issue, wf)

	for _, want := range []string{
		"## Current status guidance",
		"Build relevant automated coverage before handoff.",
		"issue-cli retrospective combat/fix-heat --body",
		"retros/ in the project",
		"Subsystem workflow for Combat:",
		"Missing system-specific instructions:",
		"issue-cli report-bug",
		"bugs/ in the server root",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q\n\n%s", want, prompt)
		}
	}
}

// --- handleDetail ---

func TestHandleDetail_ReturnsIssueDetail(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/issue/bug-in-login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleDetail_404ForUnknownSlug(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/issue/nonexistent-issue")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHandleDetail_BackURLFromBoard(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/issue/bug-in-login?from=board")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHandleDetail_ShowsActiveSessionDetails(t *testing.T) {
	proj, _ := setupTestProject(t)
	withMockTmuxSessions(t, []AgentSession{
		{Name: "agent-bug-in-login", StartTime: "2026-04-02 21:57:59"},
		{Name: "agent-unrelated-work", StartTime: "2026-04-02 21:58:59"},
	})

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/issue/bug-in-login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{"1 active bot", "Active Agent", "agent-bug-in-login", "2026-04-02 21:57:59"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected detail view to contain %q\n%s", want, html)
		}
	}
}

func TestHandleIssuesJSON_IncludesActiveSessions(t *testing.T) {
	proj, _ := setupTestProject(t)
	withMockTmuxSessions(t, []AgentSession{{Name: "agent-bug-in-login", StartTime: "2026-04-02 21:57:59"}})

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/issues.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var issues []issueJSON
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		t.Fatal(err)
	}

	for _, issue := range issues {
		if issue.Slug == "bug-in-login" {
			if !issue.HasActiveAgent {
				t.Fatal("expected bug-in-login to be marked active")
			}
			if len(issue.ActiveSessions) != 1 || issue.ActiveSessions[0].Name != "agent-bug-in-login" {
				t.Fatalf("unexpected active sessions: %+v", issue.ActiveSessions)
			}
			return
		}
	}
	t.Fatal("bug-in-login missing from issues.json")
}

func TestHandleHash_ChangesWhenActiveSessionsChange(t *testing.T) {
	proj, _ := setupTestProject(t)

	fetchHash := func(sessions []AgentSession) string {
		withMockTmuxSessions(t, sessions)
		ts := newTestServer(t, []tracker.Project{proj})
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/p/test-project/hash")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var payload map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		return payload["hash"]
	}

	hashWithout := fetchHash(nil)
	hashWith := fetchHash([]AgentSession{{Name: "agent-bug-in-login", StartTime: "2026-04-02 21:57:59"}})
	if hashWithout == hashWith {
		t.Fatal("expected hash to change when active sessions change")
	}
}

func TestSessionMatchesIssue(t *testing.T) {
	tests := []struct {
		name        string
		sessionName string
		slug        string
		want        bool
	}{
		{name: "normalized dispatch session", sessionName: "agent-api-integrate-with-claude-session-names-to-show-active-agent-work", slug: "api/integrate-with-claude-session-names-to-show-active-agent-work", want: true},
		{name: "plain slug fragment", sessionName: "claude-watch-bug-in-login", slug: "bug-in-login", want: true},
		{name: "different issue", sessionName: "agent-something-else", slug: "bug-in-login", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionMatchesIssue(tt.sessionName, tt.slug); got != tt.want {
				t.Fatalf("sessionMatchesIssue(%q, %q) = %v, want %v", tt.sessionName, tt.slug, got, tt.want)
			}
		})
	}
}

// --- handleUpdateIssue ---

func TestHandleUpdateIssue_UpdatesStatus(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	body := `{"status":"done"}`
	resp, err := http.Post(ts.URL+"/p/test-project/issue/bug-in-login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", result)
	}

	// Verify the file was updated
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	for _, issue := range issues {
		if issue.Slug == "bug-in-login" {
			if issue.Status != "done" {
				t.Fatalf("expected status 'done', got '%s'", issue.Status)
			}
			return
		}
	}
	t.Fatal("issue not found after update")
}

func TestHandleUpdateIssue_UpdatesPriority(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	body := `{"priority":"critical"}`
	resp, err := http.Post(ts.URL+"/p/test-project/issue/bug-in-login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	issues, _ := tracker.LoadIssues(proj.IssueDir)
	for _, issue := range issues {
		if issue.Slug == "bug-in-login" {
			if issue.Priority != "critical" {
				t.Fatalf("expected priority 'critical', got '%s'", issue.Priority)
			}
			return
		}
	}
	t.Fatal("issue not found after update")
}

func TestHandleUpdateIssue_UpdatesAssignee(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	assignee := "charlie"
	body := `{"assignee":"charlie"}`
	resp, err := http.Post(ts.URL+"/p/test-project/issue/bug-in-login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	issues, _ := tracker.LoadIssues(proj.IssueDir)
	for _, issue := range issues {
		if issue.Slug == "bug-in-login" {
			if issue.Assignee != assignee {
				t.Fatalf("expected assignee '%s', got '%s'", assignee, issue.Assignee)
			}
			return
		}
	}
	t.Fatal("issue not found after update")
}

func TestHandleUpdateIssue_404ForUnknownSlug(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	body := `{"status":"done"}`
	resp, err := http.Post(ts.URL+"/p/test-project/issue/nonexistent", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHandleDetail_ShowsEditInNvimAction(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/issue/bug-in-login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Edit in nvim") {
		t.Fatalf("detail page missing Edit in nvim action: %s", body)
	}
}

func TestHandleEditIssueInNvim_UsesLauncherAndPreservesComments(t *testing.T) {
	proj, _ := setupTestProject(t)
	issuePath := filepath.Join(proj.IssueDir, "bug-in-login.md")
	if err := tracker.AddComment(issuePath, 0, "Keep me", "app"); err != nil {
		t.Fatal(err)
	}

	origLauncher := launchIssueBodyEditor
	t.Cleanup(func() { launchIssueBodyEditor = origLauncher })
	launchIssueBodyEditor = func(proj *tracker.Project, issue *tracker.Issue) (BodyEditResponse, error) {
		updated := "Edited in nvim"
		if err := tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Body: &updated}); err != nil {
			return BodyEditResponse{}, err
		}
		return BodyEditResponse{Status: "launched", Session: "agent-bug-in-login-edit", Message: "Opened in nvim"}, nil
	}

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/p/test-project/issue/bug-in-login/edit-in-nvim", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result BodyEditResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "launched" {
		t.Fatalf("expected launched status, got %#v", result)
	}

	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.Slug == "bug-in-login" {
			if issue.BodyRaw != "Edited in nvim" {
				t.Fatalf("expected updated body, got %q", issue.BodyRaw)
			}
			comments, err := tracker.LoadComments(issue.FilePath)
			if err != nil {
				t.Fatal(err)
			}
			if len(comments) != 1 || comments[0].Text != "Keep me" {
				t.Fatalf("expected preserved comments, got %#v", comments)
			}
			return
		}
	}
	t.Fatal("issue not found after nvim edit")
}

func TestHandleEditIssueInNvim_404ForUnknownSlug(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/p/test-project/issue/nonexistent/edit-in-nvim", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHandleUpdateIssue_BadJSON(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/p/test-project/issue/bug-in-login", "application/json", strings.NewReader("{bad"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// --- handleDeleteIssue ---

func TestHandleDeleteIssue_DeletesIssueFile(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/p/test-project/issue/fix-typo/delete", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify file is gone
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	for _, issue := range issues {
		if issue.Slug == "fix-typo" {
			t.Fatal("issue should have been deleted")
		}
	}
}

func TestHandleDeleteIssue_404ForUnknownSlug(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/p/test-project/issue/nonexistent/delete", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// --- handleCreateIssue ---

func TestHandleCreateIssue_CreatesNewIssue(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	body := `{"title":"New feature request","status":"idea","system":"UI"}`
	resp, err := http.Post(ts.URL+"/p/test-project/issues/create", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", result)
	}
	if result["slug"] == "" {
		t.Fatal("expected slug in response")
	}

	// Verify the file exists
	issues, _ := tracker.LoadIssues(proj.IssueDir)
	found := false
	for _, issue := range issues {
		if issue.Title == "New feature request" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("created issue not found")
	}
}

func TestHandleCreateIssue_DefaultsStatusToIdea(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	body := `{"title":"Minimal issue"}`
	resp, err := http.Post(ts.URL+"/p/test-project/issues/create", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	issues, _ := tracker.LoadIssues(proj.IssueDir)
	for _, issue := range issues {
		if issue.Title == "Minimal issue" {
			if issue.Status != "idea" {
				t.Fatalf("expected default status 'idea', got '%s'", issue.Status)
			}
			return
		}
	}
	t.Fatal("created issue not found")
}

func TestHandleCreateIssue_RequiresTitle(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	body := `{"title":"","status":"idea"}`
	resp, err := http.Post(ts.URL+"/p/test-project/issues/create", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleCreateIssue_BadJSON(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/p/test-project/issues/create", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// --- handleAddComment ---

func TestHandleAddComment_AddsComment(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	body := `{"block":0,"text":"This is a test comment"}`
	resp, err := http.Post(ts.URL+"/p/test-project/issue/bug-in-login/comments", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	// Verify comment was saved
	issuePath := filepath.Join(proj.IssueDir, "bug-in-login.md")
	comments, err := tracker.LoadComments(issuePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].Text != "This is a test comment" {
		t.Fatalf("expected comment text 'This is a test comment', got '%s'", comments[0].Text)
	}
}

func TestHandleAddComment_RequiresText(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	body := `{"block":0,"text":""}`
	resp, err := http.Post(ts.URL+"/p/test-project/issue/bug-in-login/comments", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleAddComment_404ForUnknownIssue(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	body := `{"block":0,"text":"comment"}`
	resp, err := http.Post(ts.URL+"/p/test-project/issue/nonexistent/comments", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// --- handleGetComments ---

func TestHandleGetComments_ReturnsEmptyArray(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/issue/bug-in-login/comments")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var comments []tracker.Comment
	json.NewDecoder(resp.Body).Decode(&comments)
	if len(comments) != 0 {
		t.Fatalf("expected 0 comments, got %d", len(comments))
	}
}

func TestHandleGetComments_ReturnsExistingComments(t *testing.T) {
	proj, _ := setupTestProject(t)

	// Add a comment directly to the file
	issuePath := filepath.Join(proj.IssueDir, "bug-in-login.md")
	tracker.AddComment(issuePath, 0, "Existing comment", "test")

	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/issue/bug-in-login/comments")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var comments []tracker.Comment
	json.NewDecoder(resp.Body).Decode(&comments)
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].Text != "Existing comment" {
		t.Fatalf("expected 'Existing comment', got '%s'", comments[0].Text)
	}
}

func TestHandleGetComments_404ForUnknownIssue(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/test-project/issue/nonexistent/comments")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// --- handleProjectRoutes: unknown project ---

func TestHandleProjectRoutes_404ForUnknownProject(t *testing.T) {
	proj, _ := setupTestProject(t)
	ts := newTestServer(t, []tracker.Project{proj})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/p/unknown-project/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// --- filterIssues unit tests ---

func TestFilterIssues(t *testing.T) {
	issues := []*tracker.Issue{
		{Title: "Bug A", Status: "in progress", System: "Auth", Priority: "high", Labels: []string{"bug"}, Assignee: "alice", BodyRaw: "auth login bug"},
		{Title: "Feature B", Status: "backlog", System: "UI", Priority: "medium", Labels: []string{"enhancement"}, Assignee: "bob", BodyRaw: "dark mode"},
		{Title: "Bug C", Status: "done", System: "Auth", Priority: "low", Labels: []string{"bug", "docs"}, Assignee: "", BodyRaw: "typo fix"},
		{Title: "Feature D", Status: "idea", System: "API", Priority: "critical", Labels: []string{"enhancement", "api"}, Assignee: "alice", BodyRaw: "new endpoint"},
	}

	tests := []struct {
		name     string
		filter   FilterParams
		expected int
	}{
		{
			name:     "no filter returns all",
			filter:   FilterParams{},
			expected: 4,
		},
		{
			name:     "filter by status",
			filter:   FilterParams{Status: "in progress"},
			expected: 1,
		},
		{
			name:     "filter by system (case insensitive)",
			filter:   FilterParams{System: "auth"},
			expected: 2,
		},
		{
			name:     "filter by priority",
			filter:   FilterParams{Priority: "high"},
			expected: 1,
		},
		{
			name:     "filter by label",
			filter:   FilterParams{Label: "bug"},
			expected: 2,
		},
		{
			name:     "filter by label case insensitive",
			filter:   FilterParams{Label: "BUG"},
			expected: 2,
		},
		{
			name:     "filter by assignee",
			filter:   FilterParams{Assignee: "alice"},
			expected: 2,
		},
		{
			name:     "filter by assignee _claimed",
			filter:   FilterParams{Assignee: "_claimed"},
			expected: 3,
		},
		{
			name:     "filter by assignee _unclaimed",
			filter:   FilterParams{Assignee: "_unclaimed"},
			expected: 1,
		},
		{
			name:     "search in title",
			filter:   FilterParams{Search: "Bug"},
			expected: 2,
		},
		{
			name:     "search in body",
			filter:   FilterParams{Search: "dark mode"},
			expected: 1,
		},
		{
			name:     "search case insensitive",
			filter:   FilterParams{Search: "AUTH"},
			expected: 1,
		},
		{
			name:     "combined status and system",
			filter:   FilterParams{Status: "in progress", System: "Auth"},
			expected: 1,
		},
		{
			name:     "combined filters no match",
			filter:   FilterParams{Status: "done", System: "UI"},
			expected: 0,
		},
		{
			name:     "filter by nonexistent label",
			filter:   FilterParams{Label: "nonexistent"},
			expected: 0,
		},
		{
			name:     "combined assignee and priority",
			filter:   FilterParams{Assignee: "alice", Priority: "critical"},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterIssues(issues, tt.filter)
			if len(result) != tt.expected {
				t.Errorf("expected %d issues, got %d", tt.expected, len(result))
			}
		})
	}
}
