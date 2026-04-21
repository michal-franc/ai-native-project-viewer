# Issue Viewer

A Go web app that renders markdown issue files as a GitHub-style project tracker with list, kanban board, and documentation views.

## Running

```bash
go build && ./issue-viewer -dir ./issues -docs ./docs -port 8080
```

Flags:

- `-dir` — issue markdown directory (default `./issues`), supports subdirectories
- `-docs` — documentation markdown directory (default `./docs`)
- `-port` — HTTP port (default `8080`)

## Project Structure

```
main.go          — entry point, CLI flags, starts HTTP server
handlers.go      — HTTP handlers, routing, template functions, Server struct
issue.go         — Issue struct, ParseIssue, LoadIssues (walks subdirs)
docs.go          — DocPage struct, ParseDocPage, LoadDocs
templates/       — Go HTML templates (list.html, board.html, detail.html, docs.html)
static/style.css — all CSS (dark GitHub theme)
sync-issues.sh   — downloads issues from GitHub Project into ./issues/<System>/
```

## Issue File Format

Issues are markdown files with YAML frontmatter stored in `./issues/` or `./issues/<System>/` subdirectories.

```markdown
---
title: "Issue title"
status: "in progress"
system: "Combat"
version: "0.1"
labels:
  - bug
  - enhancement
priority: "high"
created: "2025-01-15"
number: 42
repo: "owner/repo"
---

Markdown body here. Supports `[x]` checkboxes.
```

### Required fields

- `title` — issue title

### Optional fields

- `status` — one of: `idea`, `in design`, `backlog`, `in progress`, `testing`, `documentation`, `done`, `none`
- `system` — categorization tag (also used as subdirectory name by sync script)
- `version` — version string, filterable on the board view
- `labels` — list of label strings
- `priority` — one of: `low`, `medium`, `high`, `critical`
- `created` — date string for sorting (newest first)
- `number` — GitHub issue number (used in board card display as `#number`)
- `repo` — GitHub repo in `owner/repo` format

### Custom fields

Any other frontmatter key is preserved as a custom field and displayed in the detail view sidebar. String values that start with `http://` or `https://` render as clickable links. List values render as bullet lists. Example:

```yaml
pr: "https://github.com/org/repo/pull/456"   # renders as a link
pr_author: "jsmith"                            # renders as text
participants:                                  # renders as a list
  - alice
  - bob
```

### Filename convention

- For GitHub issues: `<number>.md` (e.g., `42.md`)
- For draft issues: slugified title (e.g., `my-feature-idea.md`)
- The filename without `.md` becomes the slug used in URLs (`/issue/<slug>`)

## Doc Page Format

Documentation pages are markdown files in the docs directory.

```markdown
---
title: "Page Title"
order: 1
---

Page content in markdown.
```

- `title` — defaults to titlecased filename if omitted
- `order` — numeric sort order (lower first), pages with same order sort alphabetically
- Frontmatter is optional; a plain markdown file works too

## Syncing from GitHub Projects

```bash
./sync-issues.sh [output-dir]
```

Downloads all items from `github.com/users/michal-franc/projects/4` and writes them as markdown files to `./issues/<System>/`. Cleans the output directory before writing.

## Views

- `/` — filterable issue list (status, system, priority, label, search)
- `/board` — kanban board with status columns, version filter
- `/docs` — documentation pages with sidebar navigation
- `/issue/<slug>` — issue detail with sidebar metadata

## Agent Dispatch

The board and detail views can dispatch issues to AI agents (Claude or Codex) via tmux sessions:

- **Board view** — hover a card, click the play button, pick Claude or Codex
- **Detail view** — two buttons in the sidebar (Claude / Codex)
- **Backend** — `POST /p/<project>/issue/<slug>/dispatch` with `{"agent": "claude"}` or `{"agent": "codex"}`
- The handler creates a tmux session, opens alacritty, starts the selected agent, and pastes the generated prompt

## Workflow Side-Effects

`WorkflowStatus` supports a `side_effects` field — actions that run automatically after a transition:

- `clear_assignee` — clears the assignee field (used on `backlog` so design agents are unassigned before implementation)

Side-effects are declarative in `workflow.yaml` or the default config:

```yaml
- name: backlog
  validation: [has_checkboxes]
  side_effects: [clear_assignee]
```

## Adding New Statuses

Status colors and board column order are defined in `handlers.go`:

- `statusOrder` — column display order on the board
- `statusDescriptions` — subtitle text under each column header
- `statusColor` template function in `funcMap` — badge/dot colors
