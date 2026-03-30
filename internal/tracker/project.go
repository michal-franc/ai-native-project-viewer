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
}

// LoadWorkflow loads the project's workflow config.
// Tries: explicit WorkflowFile path → workflow.yaml in cwd → DefaultWorkflow.
func (p *Project) LoadWorkflow() *WorkflowConfig {
	if p.WorkflowFile != "" {
		wf, err := LoadWorkflow(p.WorkflowFile)
		if err == nil {
			return wf
		}
	}
	wf, err := LoadWorkflow("workflow.yaml")
	if err == nil {
		return wf
	}
	return DefaultWorkflow()
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
