package lsf

import (
	"fmt"
	"path/filepath"
	"strings"

	configyaml "github.com/buildkite/yaml"

	"github.com/drone/drone-runtime/engine"
	"github.com/drone/drone-yaml/yaml"
	"github.com/drone/drone-yaml/yaml/compiler"
	"github.com/drone/drone-yaml/yaml/linter"
)

// PipelineType recognizes the company's git/none image markers only when no
// explicit type was provided. Explicit Docker pipelines keep their semantics.
func PipelineType(p *yaml.Pipeline) string {
	if p.Type != "" {
		return p.Type
	}
	if p.Kind == "pipeline" && len(p.Steps) > 0 {
		company := true
		for _, step := range p.Steps {
			if step.Image != "git" && step.Image != "none" {
				company = false
				break
			}
		}
		if company {
			return "lsf"
		}
	}
	return "docker"
}

// ApplyEnvironment supplies pipeline-level defaults, preserving step overrides
// and from_secret references. The upstream Pipeline type omits this field.
func ApplyEnvironment(p *yaml.Pipeline, source string) error {
	resources, err := yaml.ParseRawString(source)
	if err != nil {
		return err
	}
	for _, raw := range resources {
		var item struct {
			Kind        string
			Name        string
			Environment map[string]*yaml.Variable
		}
		if err := configyaml.Unmarshal(raw.Data, &item); err != nil {
			return err
		}
		if item.Kind != p.Kind || item.Name != p.Name {
			continue
		}
		for _, step := range p.Steps {
			if step.Environment == nil {
				step.Environment = make(map[string]*yaml.Variable)
			}
			for key, value := range item.Environment {
				if _, ok := step.Environment[key]; !ok {
					step.Environment[key] = value
				}
			}
		}
		return nil
	}
	return nil
}

// Lint validates native host jobs, then reuses Drone's dependency and name checks.
func Lint(p *yaml.Pipeline, trusted bool) error {
	if !trusted {
		return fmt.Errorf("lsf: native execution requires a trusted repository")
	}
	if PipelineType(p) != "lsf" || (p.Platform.OS != "" && p.Platform.OS != "linux") {
		return fmt.Errorf("lsf: expected a Linux pipeline with type: lsf")
	}
	if len(p.Services) != 0 || len(p.Volumes) != 0 || len(p.PullSecrets) != 0 || p.Workspace.Base != "" || p.Workspace.Path != "" {
		return fmt.Errorf("lsf: services, volumes, image credentials and custom workspace are unsupported")
	}
	copyPipeline := *p
	copyPipeline.Steps = nil
	for _, s := range p.Steps {
		if (s.Image != "" && s.Image != "none" && s.Image != "git") || s.Build != nil || s.Push != nil || s.Detach || s.Privileged ||
			len(s.Volumes)+len(s.Devices)+len(s.Ports)+len(s.DNS)+len(s.DNSSearch)+len(s.ExtraHosts)+len(s.Settings)+len(s.Entrypoint)+len(s.Command) != 0 ||
			s.Network != "" || s.User != "" || s.Pull != "" || s.Resources != nil {
			return fmt.Errorf("lsf: step %q uses unsupported container settings; use commands and runner LSF resource settings", s.Name)
		}
		if s.Image == "git" {
			if s.Name != "clone" || len(s.Commands) != 0 || s.WorkingDir != "" {
				return fmt.Errorf("lsf: image git is reserved for a clone step without commands or working_dir")
			}
		} else if len(s.Commands) == 0 {
			return fmt.Errorf("lsf: step %q requires commands", s.Name)
		}
		if s.Shell != "" {
			if _, err := shellArgs(s.Shell); err != nil {
				return err
			}
		}
		if value := s.Environment["SHELL_TYPE"]; value != nil && value.Secret == "" && value.Value != "" {
			if _, err := shellArgs(value.Value); err != nil {
				return err
			}
		}
		if value := s.Environment["BSUB_OPTION"]; value != nil && value.Secret == "" {
			if _, err := splitOptions(value.Value); err != nil {
				return err
			}
		}
		if !relativePath(s.WorkingDir) {
			return fmt.Errorf("lsf: step %q working_dir must be relative to the workspace", s.Name)
		}
		copyStep := *s
		copyStep.Image = "lsf-validation-placeholder"
		copyPipeline.Steps = append(copyPipeline.Steps, &copyStep)
	}
	return linter.Lint(&copyPipeline, trusted)
}

func relativePath(path string) bool {
	clean := filepath.Clean(path)
	return !filepath.IsAbs(path) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// Compile retains Drone's conditions, secrets, dependency graph and transforms,
// replacing container configuration with native tcsh scripts in Spec.Files.
func Compile(c *compiler.Compiler, p *yaml.Pipeline) *engine.Spec {
	spec := c.Compile(p)
	spec.Files = nil
	spec.Docker = nil
	steps := make(map[string]*yaml.Container)
	for _, s := range p.Steps {
		steps[s.Name] = s
	}
	graph := false
	for _, s := range p.Steps {
		if len(s.DependsOn) > 0 {
			graph = true
		}
	}
	for _, step := range spec.Steps {
		var script string
		step.WorkingDir = ""
		source := steps[step.Metadata.Name]
		if clone := steps["clone"]; graph && clone != nil && clone.Image == "git" && step.Metadata.Name != "clone" && len(step.DependsOn) == 0 {
			step.DependsOn = []string{"clone"}
		}
		nativeClone := (step.Metadata.Name == "clone" && !p.Clone.Disable) || (source != nil && source.Image == "git")
		if nativeClone {
			depth := ""
			if p.Clone.Depth > 0 {
				depth = fmt.Sprintf(" --depth=%d", p.Clone.Depth)
			}
			script = commandScript([]string{
				"git init .",
				"git remote add origin \"$DRONE_REMOTE_URL\"",
				"git fetch --no-tags" + depth + " origin \"$DRONE_COMMIT_SHA\"",
				"git -c advice.detachedHead=false checkout --force --detach \"$DRONE_COMMIT_SHA\"",
			})
			if p.Clone.SkipVerify {
				step.Envs["GIT_SSL_NO_VERIFY"] = "true"
			}
		} else {
			script = commandScript(source.Commands)
			step.WorkingDir = source.WorkingDir
		}
		if source != nil && source.Shell != "" && step.Envs["SHELL_TYPE"] == "" {
			step.Envs["SHELL_TYPE"] = source.Shell
		}
		step.Volumes = nil
		step.Files = nil
		step.Docker = nil
		spec.Files = append(spec.Files, &engine.File{
			Metadata: engine.Metadata{Name: step.Metadata.Name, Labels: map[string]string{"lsf.drone.io/clone": fmt.Sprint(nativeClone)}}, Data: []byte(script),
		})
	}
	return spec
}

// commandScript keeps commands in one shell so variable and directory changes
// persist. Trace each YAML command as literal bytes, without shell expansion.
// Multi-line commands remain intact (including heredocs and control flow).
func commandScript(commands []string) string {
	var script strings.Builder
	for _, command := range commands {
		var encoded strings.Builder
		for _, char := range []byte(command) {
			fmt.Fprintf(&encoded, "\\%03o", char)
		}
		fmt.Fprintf(&script, "/usr/bin/printf '\\033[32m+ %s\\033[0;37m\\n'\n", encoded.String())
		script.WriteString(command)
		script.WriteByte('\n')
	}
	return script.String()
}
