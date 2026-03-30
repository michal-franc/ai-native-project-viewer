package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/michal-franc/issue-viewer/internal/tracker"
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
		version := flagValue(cmdArgs, "--version")
		if version == "" && !design {
			fatal("--version is required\n\nExample:\n  issue-cli next --version 0.1\n  issue-cli next --design --version 0.1")
		}
		proj := loadProject(configPath, projectSlug)
		runNext(proj, design, version)
	case "start":
		requireArg(cmdArgs, "start", "<slug>")
		proj := loadProject(configPath, projectSlug)
		assignee := flagValue(cmdArgs[1:], "--assignee")
		runStart(proj, cmdArgs[0], assignee)
	case "context":
		requireArg(cmdArgs, "context", "<slug>")
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
			fatal("--assignee is required\n\nExample:\n  issue-cli claim %s --assignee \"my-bot\"", cmdArgs[0])
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
			fatal("--text is required\n\nExample:\n  issue-cli comment %s --text \"your comment here\"", cmdArgs[0])
		}
		proj := loadProject(configPath, projectSlug)
		runComment(proj, cmdArgs[0], text)
	case "checklist":
		requireArg(cmdArgs, "checklist", "<slug>")
		proj := loadProject(configPath, projectSlug)
		runChecklist(proj, cmdArgs[0])
	case "list":
		proj := loadProject(configPath, projectSlug)
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
		return &tracker.Project{
			Name:     name,
			Slug:     tracker.Slugify(name),
			IssueDir: "./issues",
			DocsDir:  docsDir,
		}
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
  start <slug>         Claim an issue and get step-by-step instructions
  next --version <v>   Find work for a version (backlog + in-progress + testing)
  next --design        Find ideas and in-design issues needing design
  context <slug>       Full context dump for an issue
  create               Create a new issue
  transition <slug>    Move issue to next status (strict ordering)
  claim <slug>         Set assignee on an issue
  unclaim <slug>       Remove assignee from an issue
  done <slug>          Mark issue as done (validates and auto-unclaims)
  comment <slug>       Add a comment to an issue
  checklist <slug>     Show checkbox status for an issue
  list                 List issues with filters
  search <query>       Search across issue titles and bodies
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
  - Always claim before starting work
  - Never skip statuses — follow the order strictly
  - Always update docs before marking done
  - Reference other issues with #<slug> in the body
  - Use checkboxes [x] to track subtasks and acceptance criteria

== When you pick up an issue ==
  1. issue-cli start <slug>          — claims it, shows next steps
  2. Do the work, check off items
  3. Add ## Test Plan section with ### Automated and ### Manual
  4. issue-cli transition <slug> --to "testing"
  5. Log test results: issue-cli comment <slug> --text "tests: ..."
  6. issue-cli transition <slug> --to "documentation"
  7. Update docs: issue-cli comment <slug> --text "docs: ..."
  8. issue-cli done <slug>

== Quick start ==
  issue-cli next --version 0.1    — find work for version 0.1
  issue-cli start <slug>          — claim it and get instructions
  issue-cli done <slug>           — finish when complete

Run 'issue-cli process <topic>' for details:
  workflow, format, transitions, testing, docs, systems, references
`)
	case "workflow":
		fmt.Println("== Status Lifecycle ==")
		for i, s := range tracker.StatusOrder {
			desc := tracker.StatusDescriptions[s]
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
  backlog → in progress  Must have an assignee (use: issue-cli claim)
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

	if assignee == "" {
		assignee = "agent"
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
		fmt.Printf("✓ Claimed (assignee: %s)\n", assignee)
	} else {
		fmt.Printf("Already claimed by: %s\n", issue.Assignee)
	}

	// Auto-transition to in progress if in backlog
	if issue.Status == "backlog" {
		s := "in progress"
		err := tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Status: &s})
		if err != nil {
			fatal("Failed to transition: %v", err)
		}
		fmt.Println("✓ Status → in progress")
		issue.Status = "in progress"
	}

	fmt.Println()

	// Show state-aware guidance
	switch issue.Status {
	case "idea":
		fmt.Printf(`== Next steps ==
1. Flesh out the description — add requirements, edge cases
2. When ready: issue-cli transition %s --to "in design"
`, issue.Slug)
	case "in design":
		fmt.Printf(`== Next steps ==
1. Add acceptance criteria as checkboxes [ ] in the body
2. When ready: issue-cli transition %s --to "backlog"
`, issue.Slug)
	case "in progress":
		total, checked := tracker.CountCheckboxes(issue.BodyRaw)
		if total > 0 {
			fmt.Printf("== Checklist (%d/%d) ==\n", checked, total)
			printCheckboxes(issue.BodyRaw)
			fmt.Println()
		}
		fmt.Printf(`== Next steps ==
1. Implement the fix
2. Check off items as you complete them
3. Add a ## Test Plan section with:
   ### Automated — tests you wrote (with file paths)
   ### Manual — steps for a human to verify
4. When all checkboxes done:
   issue-cli transition %s --to "testing"
5. Log test results:
   issue-cli comment %s --text "tests: describe results"
6. issue-cli transition %s --to "documentation"
7. Update docs and log:
   issue-cli comment %s --text "docs: updated relevant-doc.md"
8. Finish:
   issue-cli done %s
`, issue.Slug, issue.Slug, issue.Slug, issue.Slug, issue.Slug)
	case "testing":
		fmt.Printf(`== Next steps ==
1. Verify tests pass, log results:
   issue-cli comment %s --text "tests: all passing"
2. Move to documentation:
   issue-cli transition %s --to "documentation"
`, issue.Slug, issue.Slug)
	case "documentation":
		fmt.Printf(`== Next steps ==
1. Update relevant docs
2. Log what was updated:
   issue-cli comment %s --text "docs: updated relevant-doc.md"
3. Finish:
   issue-cli done %s
`, issue.Slug, issue.Slug)
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
	fmt.Printf("Status: %s | System: %s | Priority: %s | Assignee: %s\n\n",
		issue.Status, issue.System, issue.Priority, issue.Assignee)

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
	if status == "" {
		status = "idea"
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
	content.WriteString("---\n\n")

	if err := os.WriteFile(filename, []byte(content.String()), 0644); err != nil {
		fatal("Failed to create issue: %v", err)
	}

	if system != "" {
		slug = strings.ToLower(system) + "/" + slug
	}

	fmt.Printf("✓ Created: %s\n", filename)
	fmt.Printf("  Slug: %s\n", slug)
	fmt.Printf("\nNext: issue-cli start %s\n", slug)
}

func runTransition(proj *tracker.Project, slug, to string) {
	issue, _ := findIssue(proj, slug)
	to = strings.ToLower(to)

	if !tracker.ValidTransition(issue.Status, to) {
		// Show what the next valid status is
		idx := tracker.StatusIndex(issue.Status)
		if idx >= 0 && idx+1 < len(tracker.StatusOrder) {
			next := tracker.StatusOrder[idx+1]
			fatal("Cannot transition from \"%s\" to \"%s\" — must go to \"%s\" next.\n\n  issue-cli transition %s --to \"%s\"",
				issue.Status, to, next, slug, next)
		}
		fatal("Cannot transition from \"%s\" to \"%s\"", issue.Status, to)
	}

	// Validate requirements for this transition
	comments, _ := tracker.LoadComments(issue.FilePath)
	validateTransition(issue, to, comments)

	// Do the transition
	s := to
	if err := tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Status: &s}); err != nil {
		fatal("Failed to transition: %v", err)
	}

	fmt.Printf("✓ %s → %s\n", issue.Status, to)
}

func validateTransition(issue *tracker.Issue, to string, comments []tracker.Comment) {
	switch to {
	case "in design":
		if strings.TrimSpace(issue.BodyRaw) == "" {
			fatal("Cannot transition to \"in design\" — issue body is empty.\n\nAdd a description to the issue body first.")
		}
	case "backlog":
		total, _ := tracker.CountCheckboxes(issue.BodyRaw)
		if total == 0 {
			fatal("Cannot transition to \"backlog\" — no acceptance criteria.\n\nAdd checkboxes [ ] to the issue body defining what done looks like:\n\n  - [ ] First requirement\n  - [ ] Second requirement")
		}
	case "in progress":
		if issue.Assignee == "" {
			fatal("Cannot transition to \"in progress\" — no assignee.\n\n  issue-cli claim %s --assignee \"your-name\"", issue.Slug)
		}
	case "testing":
		total, checked := tracker.CountCheckboxes(issue.BodyRaw)
		if total > 0 && checked < total {
			fatal("Cannot transition to \"testing\" — %d/%d checkboxes incomplete.\n\n  issue-cli checklist %s", checked, total, issue.Slug)
		}
	case "documentation":
		hasAuto, hasManual := tracker.HasTestPlan(issue.BodyRaw)
		if !hasAuto || !hasManual {
			fatal(`Cannot transition to "documentation" — missing test plan.

Add a ## Test Plan section to the issue body:

  ## Test Plan

  ### Automated
  - [x] Unit: path/to/test.cs — what it tests
  - [x] Integration: path/to/test.go — what it tests

  ### Manual
  - [ ] Step for a human to verify

Then log results:
  issue-cli comment %s --text "tests: 3 unit tests, all passing"`, issue.Slug)
		}
		if !tracker.HasCommentWithPrefix(comments, "tests:") {
			fatal("Cannot transition to \"documentation\" — no test results comment.\n\n  issue-cli comment %s --text \"tests: describe your test results\"", issue.Slug)
		}
	case "done":
		if !tracker.HasCommentWithPrefix(comments, "docs:") {
			fatal("Cannot transition to \"done\" — no docs comment.\n\n  issue-cli comment %s --text \"docs: updated relevant-doc.md\"\n  issue-cli comment %s --text \"docs: not needed, internal refactor\"", issue.Slug, issue.Slug)
		}
	}
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
	comments, _ := tracker.LoadComments(issue.FilePath)

	fmt.Println("== Validation ==")

	ok := true

	// Check checkboxes
	total, checked := tracker.CountCheckboxes(issue.BodyRaw)
	if total > 0 && checked == total {
		fmt.Printf("✓ All checkboxes checked (%d/%d)\n", checked, total)
	} else if total > 0 {
		fmt.Printf("✗ Checkboxes incomplete (%d/%d)\n", checked, total)
		ok = false
	}

	// Check test plan
	hasAuto, hasManual := tracker.HasTestPlan(issue.BodyRaw)
	if hasAuto && hasManual {
		fmt.Println("✓ Test plan present")
	} else {
		fmt.Println("✗ Missing test plan (need ### Automated and ### Manual)")
		ok = false
	}

	// Check test results comment
	if tracker.HasCommentWithPrefix(comments, "tests:") {
		fmt.Println("✓ Test results comment found")
	} else {
		fmt.Println("✗ No test results comment")
		ok = false
	}

	// Check docs comment
	if tracker.HasCommentWithPrefix(comments, "docs:") {
		fmt.Println("✓ Docs comment found")
	} else {
		fmt.Println("✗ No docs comment")
		ok = false
	}

	if !ok {
		fmt.Printf("\nCannot mark as done. Fix these first:\n")
		if total > 0 && checked < total {
			fmt.Printf("  issue-cli checklist %s\n", slug)
		}
		if !hasAuto || !hasManual {
			fmt.Println("  Add ## Test Plan with ### Automated and ### Manual to the body")
		}
		if !tracker.HasCommentWithPrefix(comments, "tests:") {
			fmt.Printf("  issue-cli comment %s --text \"tests: describe your test results\"\n", slug)
		}
		if !tracker.HasCommentWithPrefix(comments, "docs:") {
			fmt.Printf("  issue-cli comment %s --text \"docs: updated relevant-doc.md\"\n", slug)
		}
		os.Exit(1)
	}

	// Transition through remaining statuses
	status := issue.Status
	for _, next := range []string{"testing", "documentation", "done"} {
		if tracker.StatusIndex(next) > tracker.StatusIndex(status) {
			s := next
			tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Status: &s})
			status = next
		}
	}

	// Auto-unclaim
	empty := ""
	tracker.UpdateIssueFrontmatter(issue.FilePath, tracker.IssueUpdate{Assignee: &empty})

	fmt.Println("\n✓ Status → done")
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

func runList(proj *tracker.Project, args []string) {
	issues, err := tracker.LoadIssues(proj.IssueDir)
	if err != nil {
		fatal("Cannot load issues: %v", err)
	}

	status := flagValue(args, "--status")
	system := flagValue(args, "--system")
	assignee := flagValue(args, "--assignee")

	var filtered []*tracker.Issue
	for _, issue := range issues {
		if status != "" && issue.Status != status {
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
			a = " @" + issue.Assignee
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
			strings.Contains(strings.ToLower(issue.BodyRaw), q) {
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

	fmt.Println("By status:")
	for _, s := range tracker.StatusOrder {
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
