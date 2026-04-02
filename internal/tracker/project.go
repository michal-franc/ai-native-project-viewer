package tracker

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Project struct {
	Name         string `yaml:"name"`
	Slug         string `yaml:"slug"`
	IssueDir     string `yaml:"issues"`
	DocsDir      string `yaml:"docs"`
	WorkflowFile string `yaml:"workflow"`
	Version      string `yaml:"version"`
	WorkDir      string `yaml:"workdir"`
	I3Workspace  string `yaml:"i3_workspace"`
}

// LoadWorkflow loads the project's workflow config.
// Starts from DefaultWorkflow, then merges a custom file on top (if found).
// Custom files can add validations, side_effects, templates — duplicates are skipped.
func (p *Project) LoadWorkflow() *WorkflowConfig {
	base := DefaultWorkflow()

	var custom *WorkflowConfig
	if p.WorkflowFile != "" {
		custom, _ = LoadWorkflow(p.WorkflowFile)
	}
	if custom == nil {
		custom, _ = LoadWorkflow("workflow.yaml")
	}
	if custom == nil {
		return base
	}

	base.Merge(custom)
	return base
}

func (p *Project) LoadWorkflowForSystem(system string) *WorkflowConfig {
	return p.LoadWorkflow().ForSystem(system)
}

func (p *Project) LoadWorkflowForIssue(issue *Issue) *WorkflowConfig {
	if issue == nil {
		return p.LoadWorkflow()
	}
	return p.LoadWorkflowForSystem(issue.System)
}

type ProjectsConfig struct {
	Projects []Project `yaml:"projects"`
}

func LoadProjects(configPath string) ([]Project, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", configPath, err)
	}

	var cfg ProjectsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", configPath, err)
	}

	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		if p.Slug == "" {
			p.Slug = Slugify(p.Name)
		}
	}

	return cfg.Projects, nil
}
