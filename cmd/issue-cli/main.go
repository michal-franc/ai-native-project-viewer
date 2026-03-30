package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
	"gopkg.in/yaml.v3"
)

var jsonOutput bool

func main() {
	args := os.Args[1:]

	// Parse global flags
	var filtered []string
	configPath := "projects.yaml"
	projectSlug := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--config":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			}
		case "--project":
			if i+1 < len(args) {
				projectSlug = args[i+1]
				i++
			}
		default:
			filtered = append(filtered, args[i])
		}
	}

	if len(filtered) == 0 {
		printHelp()
		return
	}

	cmd := filtered[0]
	cmdArgs := filtered[1:]

	switch cmd {
	case "help":
		if len(cmdArgs) > 0 && cmdArgs[0] == "commands" {
			printHelp()
		} else {
			printHelp()
		}
	case "process":
		topic := ""
		if len(cmdArgs) > 0 {
			topic = cmdArgs[0]
		}
		runProcess(topic, configPath, projectSlug)
	case "next":
		design := false
		for _, a := range cmdArgs {
			if a == "--design" {
				design = true
			}
		}
		proj := loadProject(configPath, projectSlug)
		version := flagValue(cmdArgs, "--version")
		if version == "" {
			version = proj.Version
		}
		if version == "" && !design {
			fatal("--version is required (or set version in project.yaml)\n\nExample:\n  issue-cli next --version 0.1\n  issue-cli next --design --version 0.1")
		}
		runNext(proj, design, version)
	case "start":
		requireArg(cmdArgs, "start", "<slug>")
		proj := loadProject(configPath, projectSlug)
		assignee := flagValue(cmdArgs[1:], "--assignee")
		runStart(proj, cmdArgs[0], assignee)
	case "context", "show":
		requireArg(cmdArgs, cmd, "<slug>")
		proj := loadProject(configPath, projectSlug)
		runContext(proj, cmdArgs[0])
	case "create":
		proj := loadProject(configPath, projectSlug)
		runCreate(proj, cmdArgs)
	case "transition":
		requireArg(cmdArgs, "transition", "<slug>")
		to := flagValue(cmdArgs[1:], "--to")
		if to == "" {
			fatal("--to is required\n\nExample:\n  issue-cli transition %s --to \"testing\"", cmdArgs[0])
		}
		proj := loadProject(configPath, projectSlug)
		runTransition(proj, cmdArgs[0], to)
	case "claim":
		requireArg(cmdArgs, "claim", "<slug>")
		assignee := flagValue(cmdArgs[1:], "--assignee")
		if assignee == "" {
			assignee = agentNameForSlug(cmdArgs[0])
		}
		proj := loadProject(configPath, projectSlug)
		runClaim(proj, cmdArgs[0], assignee)
	case "unclaim":
		requireArg(cmdArgs, "unclaim", "<slug>")
		proj := loadProject(configPath, projectSlug)
		runUnclaim(proj, cmdArgs[0])
	case "done":
		requireArg(cmdArgs, "done", "<slug>")
		proj := loadProject(configPath, projectSlug)
		runDone(proj, cmdArgs[0])
	case "comment":
		requireArg(cmdArgs, "comment", "<slug>")
		text := flagValue(cmdArgs[1:], "--text")
		if text == "" {
			text = flagValue(cmdArgs[1:], "--body")
		}
		if text == "" {
			fatal("--text is required\n\nExample:\n  issue-cli comment %s --text \"your comment here\"", cmdArgs[0])
		}
		proj := loadProject(configPath, projectSlug)
		runComment(proj, cmdArgs[0], text)
	case "checklist":
		requireArg(cmdArgs, "checklist", "<slug>")
		proj := loadProject(configPath, projectSlug)
		runChecklist(proj, cmdArgs[0])
	case "check":
		requireArg(cmdArgs, "check", "<slug>")
		if len(cmdArgs) < 2 {
			fatal("check requires a query\n\nExample:\n  issue-cli check <slug> \"Code changes complete\"")
		}
		proj := loadProject(configPath, projectSlug)
		query := strings.Join(cmdArgs[1:], " ")
		runCheck(proj, cmdArgs[0], query)
	case "list":
		proj := loadProject(configPath, projectSlug)
		// Inject project version as default if --version not provided
		version := flagValue(cmdArgs, "--version")
		if version == "" && proj.Version != "" {
			cmdArgs = append(cmdArgs, "--version", proj.Version)
		}
		runList(proj, cmdArgs)
	case "search":
		requireArg(cmdArgs, "search", "<query>")
		proj := loadProject(configPath, projectSlug)
		runSearch(proj, strings.Join(cmdArgs, " "))
	case "stats":
		proj := loadProject(configPath, projectSlug)
		runStats(proj)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\nRun: issue-cli help\n", cmd)
		os.Exit(1)
	}
}

// --- Helpers ---

func loadProject(configPath, projectSlug string) *tracker.Project {
	// If local ./issues/ exists, use it directly — no config needed
	if info, err := os.Stat("./issues"); err == nil && info.IsDir() {
		docsDir := "./docs"
		if info, err := os.Stat(docsDir); err != nil || !info.IsDir() {
			docsDir = ""
		}
		// Derive project name from current directory
		cwd, _ := os.Getwd()
		name := filepath.Base(cwd)
		proj := &tracker.Project{
			Name:     name,
			Slug:     tracker.Slugify(name),
			IssueDir: "./issues",
			DocsDir:  docsDir,
		}
		// Load overrides from project.yaml or projects.yaml if present
		for _, f := range []string{"project.yaml", configPath} {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			// Try single project format first (project.yaml)
			var local tracker.Project
			if yaml.Unmarshal(data, &local) == nil {
				if local.Version != "" {
					proj.Version = local.Version
				}
				if local.WorkflowFile != "" {
					proj.WorkflowFile = local.WorkflowFile
				}
				if proj.Version != "" {
					break
				}
			}
			// Try multi-project format (projects.yaml)
			var cfg tracker.ProjectsConfig
			if yaml.Unmarshal(data, &cfg) == nil {
				for _, p := range cfg.Projects {
					if p.Version != "" {
						proj.Version = p.Version
						break
					}
					if p.WorkflowFile != "" {
						proj.WorkflowFile = p.WorkflowFile
					}
				}
			}
			if proj.Version != "" {
				break
			}
		}
		return proj
	}

	// Fall back to config file
	projects, err := tracker.LoadProjects(configPath)
	if err != nil {
		fatal("No ./issues/ directory found and cannot load config: %v\n\nEither run from a project root with an issues/ directory, or use --config <path>", err)
	}
	if projectSlug != "" {
		for i := range projects {
			if projects[i].Slug == projectSlug {
				return &projects[i]
			}
		}
		fatal("Project '%s' not found in config", projectSlug)
	}
	if len(projects) == 0 {
		fatal("No projects configured in %s", configPath)
	}
	return &projects[0]
}

func findIssue(proj *tracker.Project, slug string) (*tracker.Issue, []*tracker.Issue) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		fatal("Cannot load issues: %v", err)
	}
	for _, issue := range issues {
		if issue.Slug == slug {
			return issue, issues
		}
	}
	// Try partial match
	for _, issue := range issues {
		if strings.HasSuffix(issue.Slug, "/"+slug) || strings.Contains(issue.Slug, slug) {
			return issue, issues
		}
	}
	fatal("Issue not found: %s\n\nRun: issue-cli list", slug)
	return nil, nil
}

func agentNameForSlug(slug string) string {
	if name := os.Getenv("AGENT_NAME"); name != "" {
		return name
	}
	base := filepath.Base(slug)
	return "agent-" + base
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func requireArg(args []string, cmd, argName string) {
	if len(args) == 0 {
		fatal("%s requires %s\n\nExample:\n  issue-cli %s <slug>", cmd, argName, cmd)
	}
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

func outputJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// --- Commands ---

func printHelp() {
	fmt.Print(`== issue-cli — AI-Native Project Viewer CLI ==

Commands:
  process              Learn how this project works (run this first)
  start <slug>         *** USE THIS TO BEGIN WORK *** Claims, transitions to in-progress, shows next steps
  next --version <v>   Find work for a version (default: from project.yaml)
  next --design        Find ideas and in-design issues needing design
  context <slug>       Full context dump for an issue (alias: show)
  create               Create a new issue
  transition <slug>    Move issue to next status (strict ordering)
  claim <slug>         Only set assignee (does NOT start work — use 'start' instead)
  unclaim <slug>       Remove assignee from an issue
  done <slug>          Mark issue as done (validates and auto-unclaims)
  comment <slug>       Add a comment to an issue
  check <slug> <text>  Check off a checkbox item by text match
  checklist <slug>     Show checkbox status for an issue
  list                 List issues with filters (--status open|closed|<name>)
  search <query>       Search across issue titles, bodies, and statuses
  stats                Project health overview

Global flags:
  --config <path>      Path to projects.yaml (default: projects.yaml)
  --project <slug>     Select project (default: first in config)
  --json               Output as JSON

First time? Run these:
  1. issue-cli process
  2. issue-cli next
  3. issue-cli start <slug>
`)
}

func runProcess(topic, configPath, projectSlug string) {
	switch topic {
	case "", "all":
		fmt.Print(`== AI-Native Project Viewer ==

You are working with a markdown-based issue tracker.
Issues are .md files in issues/<System>/ directories.

== Workflow ==
Every issue follows this lifecycle:
  idea → in design → backlog → in progress → testing → documentation → done

== What each status means ==
  idea           Raw concept, just a title and rough description
  in design      Fleshing out requirements, approach, edge cases
  backlog        Designed and ready to implement
  in progress    Actively being worked on
  testing        Implementation done, verifying correctness
  documentation  Updating docs to reflect the change
  done           Shipped, tested, documented

== Rules ==
  - Always use 'start' to begin work (it claims AND transitions to in-progress)
  - Do NOT use 'claim' to begin work — it only sets assignee without starting
  - Never skip statuses — follow the order strictly
  - Always update docs before marking done
  - Reference other issues with #<slug> in the body
  - Use checkboxes [x] to track subtasks and acceptance criteria

== When you pick up an issue ==
  1. issue-cli start <slug>          — claims it, moves to in-progress, shows next steps
  2. Do the work, check off items
  3. Add ## Test Plan section with ### Automated and ### Manual
  4. issue-cli transition <slug> --to "testing"
  5. Log test results: issue-cli comment <slug> --text "tests: ..."
  6. issue-cli transition <slug> --to "documentation"
  7. Update docs: issue-cli comment <slug> --text "docs: ..."
  8. issue-cli done <slug>

== Quick start ==
  issue-cli next --version 0.1    — find work for version 0.1
  issue-cli start <slug>          — begin work (claims + starts in-progress)
  issue-cli done <slug>           — finish when complete

Run 'issue-cli process <topic>' for details:
  workflow, format, transitions, testing, docs, systems, references
`)
	case "workflow":
		proj := loadProject(configPath, projectSlug)
		wf := proj.LoadWorkflow()
		statusOrder := wf.GetStatusOrder()
		statusDescs := wf.GetStatusDescriptions()
		fmt.Println("== Status Lifecycle ==")
		for i, s := range statusOrder {
			desc := statusDescs[s]
			if i > 0 {
				fmt.Print("  → ")
			} else {
				fmt.Print("  ")
			}
			if desc != "" {
				fmt.Printf("%-15s  %s\n", s, desc)
			} else {
				fmt.Printf("%s\n", s)
			}
		}
	case "transitions":
		fmt.Print(`== Transition Rules ==

  → idea                 Title only
  idea → in design       Body must have content
  in design → backlog    At least one [ ] checkbox (acceptance criteria)
  backlog → in progress  Must have an assignee (use: issue-cli start — it claims and transitions)
  in progress → testing  All [x] checkboxes must be checked
  testing → documentation Must have ## Test Plan with ### Automated and ### Manual
                          Must have a test results comment
  documentation → done   Must have a "docs:" comment

Transitions are strict — you cannot skip statuses.
`)
	case "format":
		fmt.Print(`== Issue File Format ==

---
title: "Fix heat calculation overflow"
status: "idea"
system: "Combat"
version: "0.1"
assignee: "my-bot"
priority: "high"
labels:
  - bug
  - combat
---

Description in markdown. Supports checkboxes and #<slug> references.

== Required ==
  title — issue title (used to generate the URL slug)

== Optional ==
  status, system, version, assignee, priority, labels, created
`)
	case "testing":
		fmt.Print(`== Test Plan Convention ==

Before transitioning from testing → documentation, the issue body
must contain a ## Test Plan section with two subsections:

## Test Plan

### Automated
List tests you wrote. Include file paths and what they verify.
- [x] Unit: path/to/test.cs — what it tests
- [x] Integration: path/to/test.go — what it tests
- [ ] E2E: not applicable

### Manual
Steps for a human to perform. You design these but cannot check them off.
- [ ] Load a mech with 5+ heat sinks, fire all weapons
- [ ] Verify heat bar stays in range

Rules:
  - ### Automated must have at least one entry
  - ### Manual must have at least one entry
  - You check off ### Automated as you write tests
  - Only humans check off ### Manual items
  - A test results comment is required:
    issue-cli comment <slug> --text "tests: 3 unit tests added, all passing"
`)
	case "docs":
		fmt.Print(`== Documentation Convention ==

Before marking an issue as done, you must log what docs were updated:

  issue-cli comment <slug> --text "docs: updated combat/heat.md"
  issue-cli comment <slug> --text "docs: not needed, internal refactor"

The "docs:" prefix is required. This is an honor system — the CLI
validates that the comment exists but trusts its content.
`)
	case "systems":
		proj := loadProject(configPath, projectSlug)
		issues, _ := tracker.LoadIssues(proj.IssueDir)
		_, systems, _, _, _ := tracker.CollectFilterValues(issues)
		fmt.Println("== Available Systems ==")
		for _, s := range systems {
			fmt.Printf("  %s\n", s)
		}
	case "references":
		fmt.Print(`== Issue References ==

Use #<slug> in the issue body to link to other issues:
  See #combat/fix-heat-overflow for related work.
  This depends on #321 (looked up by filename).

References are resolved by:
  1. Full slug match (e.g. #combat/fix-heat-overflow)
  2. Title slug match (e.g. #fix-heat-overflow)
  3. Filename match (e.g. #321 if the file is 321.md)
`)
	default:
		fmt.Fprintf(os.Stderr, "Unknown topic: %s\n\nAvailable: workflow, format, transitions, testing, docs, systems, references\n", topic)
		os.Exit(1)
	}
}

func runNext(proj *tracker.Project, design bool, version string) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		fatal("Cannot load issues: %v", err)
	}

	// Filter by version
	if version != "" {
		var filtered []*tracker.Issue
		for _, issue := range issues {
			if issue.Version == version {
				filtered = append(filtered, issue)
			}
		}
		issues = filtered
	}

	priorityRank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "": 4}

	if design {
		var matches []*tracker.Issue
		for _, issue := range issues {
			if issue.Status == "idea" || issue.Status == "in design" {
				matches = append(matches, issue)
			}
		}
		sortByPriority(matches, priorityRank)

		if jsonOutput {
			outputJSON(matches)
			return
		}

		if len(matches) == 0 {
			fmt.Printf("No issues needing design work for version %s.\n", version)
			return
		}

		fmt.Printf("== Issues needing design work (v%s) ==\n", version)
		for _, issue := range matches {
			p := issue.Priority
			if p == "" {
				p = "none"
			}
			fmt.Printf("  [%-8s] %-45s %s\n", p, issue.Slug, issue.System)
		}
		fmt.Println("\nPick one: issue-cli start <slug>")
		return
	}

	// Collect backlog, in-progress, and testing issues
	var backlog, inProgress, testing []*tracker.Issue
	for _, issue := range issues {
		switch issue.Status {
		case "backlog":
			if issue.Assignee == "" {
				backlog = append(backlog, issue)
			}
		case "in progress":
			inProgress = append(inProgress, issue)
		case "testing":
			testing = append(testing, issue)
		}
	}

	sortByPriority(backlog, priorityRank)
	sortByPriority(inProgress, priorityRank)
	sortByPriority(testing, priorityRank)

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"backlog":     backlog,
			"in_progress": inProgress,
			"testing":     testing,
		})
		return
	}

	if len(backlog) == 0 && len(inProgress) == 0 && len(testing) == 0 {
		fmt.Printf("No work available for version %s. Try: issue-cli next --design --version %s\n", version, version)
		return
	}

	fmt.Printf("== Work for v%s ==\n", version)

	if len(inProgress) > 0 {
		fmt.Printf("\nIn Progress (%d):\n", len(inProgress))
		for _, issue := range inProgress {
			a := ""
			if issue.Assignee != "" {
				a = " @" + issue.Assignee
			}
			fmt.Printf("  [%-8s] %-45s %s%s\n", issue.Priority, issue.Slug, issue.System, a)
		}
	}

	if len(testing) > 0 {
		fmt.Printf("\nTesting (%d):\n", len(testing))
		for _, issue := range testing {
			a := ""
			if issue.Assignee != "" {
				a = " @" + issue.Assignee
			}
			fmt.Printf("  [%-8s] %-45s %s%s\n", issue.Priority, issue.Slug, issue.System, a)
		}
	}

	if len(backlog) > 0 {
		fmt.Printf("\nBacklog — unclaimed (%d):\n", len(backlog))
		for _, issue := range backlog {
			p := issue.Priority
			if p == "" {
				p = "none"
			}
			fmt.Printf("  [%-8s] %-45s %s\n", p, issue.Slug, issue.System)
		}
	}

	fmt.Println("\nPick one: issue-cli start <slug>")
}

func runStart(proj *tracker.Project, slug, assignee string) {
	issue, _ := findIssue(proj, slug)
	wf := proj.LoadWorkflow()

	if assignee == "" {
		assignee = agentNameForSlug(slug)
	}

	fmt.Printf("== Starting work on: %s ==\n", issue.Title)
	fmt.Printf("Status: %s\n", issue.Status)

	// Auto-claim if not claimed
	if issue.Assignee == "" {
		a := assignee
		err := tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Assignee: &a})
		if err != nil {
			fatal("Failed to claim: %v", err)
		}
		issue.Assignee = assignee
		fmt.Printf("✓ Claimed (assignee: %s)\n", assignee)
	} else {
		fmt.Printf("Already claimed by: %s\n", issue.Assignee)
	}

	// Auto-transition to in progress if in backlog
	if issue.Status == "backlog" {
		s := "in progress"
		update := tracker.IssueUpdate{Status: &s}

		// Append template for "in progress"
		newBody, appended := wf.AppendTemplate(issue.BodyRaw, "in progress")
		if appended {
			update.Body = &newBody
			issue.BodyRaw = newBody
		}

		err := tracker.UpdateIssueFrontmatter(issue.FilePath, update)
		if err != nil {
			fatal("Failed to transition: %v", err)
		}
		fmt.Println("✓ Status → in progress")
		if appended {
			fmt.Println("✓ Template checkboxes appended to issue body")
		}
		issue.Status = "in progress"
	}

	fmt.Println()

	printWorkflowNextSteps(wf, issue)
}

func printWorkflowNextSteps(wf *tracker.WorkflowConfig, issue *tracker.Issue) {
	total, checked := tracker.CountCheckboxes(issue.BodyRaw)
	if total > 0 {
		fmt.Printf("== Checklist (%d/%d) ==\n", checked, total)
		printCheckboxes(issue.BodyRaw)
		fmt.Println()
	}

	// Show the template for current status only if not already in the body
	tmpl := wf.TemplateForStatus(issue.Status)
	if tmpl != "" {
		firstLine := strings.SplitN(tmpl, "\n", 2)[0]
		if !strings.Contains(issue.BodyRaw, firstLine) {
			fmt.Println("== Current status template ==")
			fmt.Println(tmpl)
			fmt.Println()
		}
	}

	// Show next transition
	next := wf.NextStatus(issue.Status)
	if next != "" {
		fmt.Println("== Next ==")
		fmt.Printf("  issue-cli transition %s --to \"%s\"\n", issue.Slug, next)
	}
}

func runContext(proj *tracker.Project, slug string) {
	issue, _ := findIssue(proj, slug)

	if jsonOutput {
		comments, _ := tracker.LoadComments(issue.FilePath)
		outputJSON(map[string]interface{}{
			"issue":    issue,
			"comments": comments,
		})
		return
	}

	fmt.Printf("== %s ==\n", issue.Title)
	fmt.Printf("Status: %s | System: %s | Priority: %s | Assignee: %s\n",
		issue.Status, issue.System, issue.Priority, issue.Assignee)
	fmt.Printf("File: %s\n\n", issue.FilePath)

	fmt.Println("== Body ==")
	fmt.Println(issue.BodyRaw)
	fmt.Println()

	total, checked := tracker.CountCheckboxes(issue.BodyRaw)
	if total > 0 {
		fmt.Printf("== Checklist (%d/%d) ==\n", checked, total)
		printCheckboxes(issue.BodyRaw)
		fmt.Println()
	}

	hasAuto, hasManual := tracker.HasTestPlan(issue.BodyRaw)
	if hasAuto || hasManual {
		fmt.Print("== Test Plan ==\n")
		if hasAuto {
			fmt.Println("  ✓ ### Automated section present")
		}
		if hasManual {
			fmt.Println("  ✓ ### Manual section present")
		}
		fmt.Println()
	}

	comments, _ := tracker.LoadComments(issue.FilePath)
	if len(comments) > 0 {
		fmt.Printf("== Comments (%d) ==\n", len(comments))
		for _, c := range comments {
			status := ""
			if c.Status == "done" {
				status = " [done]"
			}
			fmt.Printf("  [%s]%s %s\n", c.Date, status, c.Text)
		}
	}
}

func runCreate(proj *tracker.Project, args []string) {
	title := flagValue(args, "--title")
	system := flagValue(args, "--system")
	status := flagValue(args, "--status")
	priority := flagValue(args, "--priority")

	if title == "" {
		fatal("--title is required\n\nExample:\n  issue-cli create --title \"Fix heat overflow\" --system Combat --status idea")
	}
	wf := proj.LoadWorkflow()
	statusOrder := wf.GetStatusOrder()

	if status == "" {
		// Default to first non-"none" status
		status = "idea"
		for _, s := range statusOrder {
			if s != "none" {
				status = s
				break
			}
		}
	}

	// Only allow creating issues in early statuses (before backlog)
	idx := wf.GetStatusIndex(status)
	backlogIdx := wf.GetStatusIndex("backlog")
	if backlogIdx == -1 {
		backlogIdx = 3
	}
	if idx == -1 || idx >= backlogIdx {
		var allowed []string
		for _, s := range statusOrder {
			if s == "none" {
				continue
			}
			if wf.GetStatusIndex(s) < backlogIdx {
				allowed = append(allowed, "\""+s+"\"")
			}
		}
		fatal("Cannot create issue with status \"%s\" — allowed: %s", status, strings.Join(allowed, ", "))
	}

	// Determine directory
	dir := proj.IssueDir
	if system != "" {
		dir = filepath.Join(dir, system)
		os.MkdirAll(dir, 0755)
	}

	slug := tracker.Slugify(title)
	filename := filepath.Join(dir, slug+".md")

	var content strings.Builder
	content.WriteString("---\n")
	content.WriteString(fmt.Sprintf("title: \"%s\"\n", strings.ReplaceAll(title, "\"", "\\\"")))
	content.WriteString(fmt.Sprintf("status: \"%s\"\n", status))
	if system != "" {
		content.WriteString(fmt.Sprintf("system: \"%s\"\n", system))
	}
	if priority != "" {
		content.WriteString(fmt.Sprintf("priority: \"%s\"\n", priority))
	}
	content.WriteString("---\n")

	// Append template for the initial status
	tmpl := wf.TemplateForStatus(status)
	if tmpl != "" {
		content.WriteString("\n")
		content.WriteString(tmpl)
		content.WriteString("\n")
	} else {
		content.WriteString("\n")
	}

	if err := os.WriteFile(filename, []byte(content.String()), 0644); err != nil {
		fatal("Failed to create issue: %v", err)
	}

	if system != "" {
		slug = strings.ToLower(system) + "/" + slug
	}

	fmt.Printf("✓ Created: %s\n", filename)
	fmt.Printf("  Slug: %s\n", slug)
	if tmpl != "" {
		fmt.Println("✓ Template checkboxes added to issue body")
	}
	fmt.Printf("\nNext: issue-cli start %s\n", slug)
}

func runTransition(proj *tracker.Project, slug, to string) {
	issue, _ := findIssue(proj, slug)
	wf := proj.LoadWorkflow()
	to = strings.ToLower(to)

	if !wf.IsValidTransition(issue.Status, to) {
		next := wf.NextStatus(issue.Status)
		if next != "" {
			fatal("Cannot transition from \"%s\" to \"%s\" — must go to \"%s\" next.\n\n  issue-cli transition %s --to \"%s\"",
				issue.Status, to, next, slug, next)
		}
		fatal("Cannot transition from \"%s\" to \"%s\"", issue.Status, to)
	}

	// Validate requirements for this transition
	comments, _ := tracker.LoadComments(issue.FilePath)
	if err := wf.Validate(issue, to, comments); err != nil {
		fatal("Cannot transition to \"%s\" — %s", to, err)
	}

	// Build update: status + template append
	s := to
	update := tracker.IssueUpdate{Status: &s}

	newBody, appended := wf.AppendTemplate(issue.BodyRaw, to)
	if appended {
		update.Body = &newBody
		issue.BodyRaw = newBody
	}

	if err := tracker.UpdateIssueFrontmatter(issue.FilePath, update); err != nil {
		fatal("Failed to transition: %v", err)
	}

	fmt.Printf("✓ %s → %s\n", issue.Status, to)
	if appended {
		fmt.Println("✓ Template checkboxes appended to issue body")
	}
	fmt.Println()

	// Show next steps for the new status
	issue.Status = to
	printWorkflowNextSteps(wf, issue)
}

func runClaim(proj *tracker.Project, slug, assignee string) {
	issue, _ := findIssue(proj, slug)

	if issue.Assignee != "" && issue.Assignee != assignee {
		// Check for --force
		for _, a := range os.Args {
			if a == "--force" {
				goto doClaim
			}
		}
		fatal("Already claimed by \"%s\"\n\nUse --force to reassign:\n  issue-cli claim %s --assignee %s --force",
			issue.Assignee, slug, assignee)
	}

doClaim:
	if err := tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Assignee: &assignee}); err != nil {
		fatal("Failed to claim: %v", err)
	}
	fmt.Printf("✓ Claimed: %s (assignee: %s)\n", issue.Slug, assignee)
}

func runUnclaim(proj *tracker.Project, slug string) {
	issue, _ := findIssue(proj, slug)
	empty := ""
	if err := tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Assignee: &empty}); err != nil {
		fatal("Failed to unclaim: %v", err)
	}
	fmt.Printf("✓ Unclaimed: %s\n", issue.Slug)
}

func runDone(proj *tracker.Project, slug string) {
	issue, _ := findIssue(proj, slug)
	wf := proj.LoadWorkflow()
	comments, _ := tracker.LoadComments(issue.FilePath)

	fmt.Println("== Validation ==")

	// Validate all remaining statuses up to "done"
	ok := true
	statusOrder := wf.GetStatusOrder()
	currentIdx := wf.GetStatusIndex(issue.Status)
	doneIdx := wf.GetStatusIndex("done")

	if doneIdx == -1 {
		fatal("No \"done\" status defined in workflow")
	}

	if currentIdx < doneIdx-1 {
		expected := statusOrder[doneIdx-1]
		fatal("Cannot mark as done from \"%s\" — issue must be in \"%s\" first.\n\n  issue-cli transition %s --to \"%s\"",
			issue.Status, expected, slug, wf.NextStatus(issue.Status))
	}

	// Check all validations from current+1 through done
	for i := currentIdx + 1; i <= doneIdx; i++ {
		st := statusOrder[i]
		if err := wf.Validate(issue, st, comments); err != nil {
			fmt.Printf("✗ %s: %s\n", st, err)
			ok = false
		} else {
			fmt.Printf("✓ %s: all checks passed\n", st)
		}
	}

	if !ok {
		fmt.Println("\nCannot mark as done. Fix the issues above first.")
		os.Exit(1)
	}

	// Transition through remaining statuses, appending templates along the way
	status := issue.Status
	for i := currentIdx + 1; i <= doneIdx; i++ {
		next := statusOrder[i]
		s := next
		update := tracker.IssueUpdate{Status: &s}

		newBody, appended := wf.AppendTemplate(issue.BodyRaw, next)
		if appended {
			update.Body = &newBody
			issue.BodyRaw = newBody
		}

		tracker.UpdateIssueFrontmatter(issue.FilePath, update)
		status = next
	}

	// Auto-unclaim
	empty := ""
	tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Assignee: &empty})

	fmt.Printf("\n✓ Status → %s\n", status)
	fmt.Println("✓ Assignee cleared")
}

func runComment(proj *tracker.Project, slug, text string) {
	issue, _ := findIssue(proj, slug)

	if err := tracker.AddComment(issue.FilePath, 0, text, "cli"); err != nil {
		fatal("Failed to add comment: %v", err)
	}
	fmt.Printf("✓ Comment added to %s\n", issue.Slug)
}

func runChecklist(proj *tracker.Project, slug string) {
	issue, _ := findIssue(proj, slug)
	total, checked := tracker.CountCheckboxes(issue.BodyRaw)

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"total": total, "checked": checked,
		})
		return
	}

	fmt.Printf("== Checklist (%d/%d) ==\n", checked, total)
	printCheckboxes(issue.BodyRaw)
}

func runCheck(proj *tracker.Project, slug, query string) {
	issue, _ := findIssue(proj, slug)

	newBody, found := tracker.CheckCheckbox(issue.BodyRaw, query)
	if !found {
		fmt.Printf("No unchecked item matching \"%s\"\n\n", query)
		fmt.Println("Unchecked items:")
		printCheckboxes(issue.BodyRaw)
		os.Exit(1)
	}

	err := tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Body: &newBody})
	if err != nil {
		fatal("Failed to update: %v", err)
	}

	total, checked := tracker.CountCheckboxes(newBody)
	fmt.Printf("✓ Checked: \"%s\"\n", query)
	fmt.Printf("  Progress: %d/%d\n", checked, total)
}

func runList(proj *tracker.Project, args []string) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		fatal("Cannot load issues: %v", err)
	}

	status := flagValue(args, "--status")
	system := flagValue(args, "--system")
	if system == "" {
		system = flagValue(args, "--category")
	}
	assignee := flagValue(args, "--assignee")
	version := flagValue(args, "--version")

	var filtered []*tracker.Issue
	for _, issue := range issues {
		if status != "" {
			switch strings.ToLower(status) {
			case "open":
				if issue.Status == "done" {
					continue
				}
			case "closed":
				if issue.Status != "done" {
					continue
				}
			default:
				if !strings.EqualFold(issue.Status, status) {
					continue
				}
			}
		}
		if version != "" && issue.Version != version {
			continue
		}
		if system != "" && !strings.EqualFold(issue.System, system) {
			continue
		}
		if assignee != "" && issue.Assignee != assignee {
			continue
		}
		filtered = append(filtered, issue)
	}

	if jsonOutput {
		outputJSON(filtered)
		return
	}

	for _, issue := range filtered {
		a := ""
		if issue.Assignee != "" {
			a = " claimed by " + issue.Assignee
		}
		fmt.Printf("  [%-13s] %-45s %-10s%s\n", issue.Status, issue.Slug, issue.System, a)
	}
	fmt.Printf("\n%d issues\n", len(filtered))
}

func runSearch(proj *tracker.Project, query string) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		fatal("Cannot load issues: %v", err)
	}

	q := strings.ToLower(query)
	var matches []*tracker.Issue
	for _, issue := range issues {
		if strings.Contains(strings.ToLower(issue.Title), q) ||
			strings.Contains(strings.ToLower(issue.BodyRaw), q) ||
			strings.Contains(strings.ToLower(issue.Status), q) {
			matches = append(matches, issue)
		}
	}

	if jsonOutput {
		outputJSON(matches)
		return
	}

	if len(matches) == 0 {
		fmt.Printf("No issues matching \"%s\"\n", query)
		return
	}

	fmt.Printf("== Search: \"%s\" (%d results) ==\n", query, len(matches))
	for _, issue := range matches {
		fmt.Printf("  [%-13s] %-45s %s\n", issue.Status, issue.Slug, issue.System)
	}
}

func runStats(proj *tracker.Project) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		fatal("Cannot load issues: %v", err)
	}

	byStatus := map[string]int{}
	bySystem := map[string]int{}
	byAssignee := map[string]int{}
	for _, issue := range issues {
		byStatus[issue.Status]++
		if issue.System != "" {
			bySystem[issue.System]++
		}
		if issue.Assignee != "" {
			byAssignee[issue.Assignee]++
		}
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"total":      len(issues),
			"by_status":  byStatus,
			"by_system":  bySystem,
			"by_assignee": byAssignee,
		})
		return
	}

	fmt.Printf("== Project Stats (%d issues) ==\n\n", len(issues))

	wf := proj.LoadWorkflow()
	fmt.Println("By status:")
	for _, s := range wf.GetStatusOrder() {
		if n, ok := byStatus[s]; ok {
			fmt.Printf("  %-15s %d\n", s, n)
		}
	}

	fmt.Println("\nBy system:")
	for sys, n := range bySystem {
		fmt.Printf("  %-15s %d\n", sys, n)
	}

	if len(byAssignee) > 0 {
		fmt.Println("\nBy assignee:")
		for a, n := range byAssignee {
			fmt.Printf("  %-15s %d\n", a, n)
		}
	}
}

// --- Utility ---

func printCheckboxes(body string) {
	re := regexp.MustCompile(`^(\s*-\s*\[[ xX]\].*)$`)
	for _, line := range strings.Split(body, "\n") {
		if re.MatchString(line) {
			fmt.Println(strings.TrimSpace(line))
		}
	}
}

func sortByPriority(issues []*tracker.Issue, ranks map[string]int) {
	for i := 0; i < len(issues); i++ {
		for j := i + 1; j < len(issues); j++ {
			ri := ranks[issues[i].Priority]
			rj := ranks[issues[j].Priority]
			if rj < ri {
				issues[i], issues[j] = issues[j], issues[i]
			}
		}
	}
}
