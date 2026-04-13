// Package bootstrap renders the shell script that providers inject into a
// fresh tray to download the cattery agent and start it.
//
// The script content is provider-agnostic. Each provider delivers it via its
// native mechanism: GCE -> startup-script metadata, Docker -> container stdin,
// future cloud providers -> user-data / custom-data.
package bootstrap

import (
	"bytes"
	"cattery/lib/config"
	"embed"
	"fmt"
	"text/template"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// Defaults applied when the user leaves a BootstrapConfig field empty.
const (
	DefaultOS           = "linux"
	DefaultAgentFolder  = "/opt/cattery"
	DefaultRunnerFolder = "/opt/cattery/actions-runner"
)

// Params are the runtime values substituted into the bootstrap template.
type Params struct {
	ServerURL    string
	AgentID      string
	AgentFolder  string
	RunnerFolder string
	User         string
}

// Generate renders the bootstrap script for the given config + params.
//
// If cfg.Script is non-empty it is parsed as a text/template. Otherwise the
// built-in template for cfg.OS is used. Empty string fields fall back to
// package-level defaults; cfg.User is left empty by default (script runs as
// whatever user the provider's delivery mechanism uses).
func Generate(cfg config.BootstrapConfig, p Params) (string, error) {
	if p.AgentFolder == "" {
		p.AgentFolder = orDefault(cfg.AgentFolder, DefaultAgentFolder)
	}
	if p.RunnerFolder == "" {
		p.RunnerFolder = orDefault(cfg.RunnerFolder, DefaultRunnerFolder)
	}
	if p.User == "" {
		p.User = cfg.User // empty allowed -> template skips sudo
	}

	tmplSrc, err := selectTemplate(cfg)
	if err != nil {
		return "", err
	}

	tmpl, err := template.New("bootstrap").Parse(tmplSrc)
	if err != nil {
		return "", fmt.Errorf("parse bootstrap template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return "", fmt.Errorf("render bootstrap template: %w", err)
	}
	return buf.String(), nil
}

// RunnerFolderOrDefault returns the bootstrap runner folder that providers
// should pass to the agent's --runner-folder flag.
func RunnerFolderOrDefault(cfg config.BootstrapConfig) string {
	return orDefault(cfg.RunnerFolder, DefaultRunnerFolder)
}

func selectTemplate(cfg config.BootstrapConfig) (string, error) {
	if cfg.Script != "" {
		return cfg.Script, nil
	}
	osName := orDefault(cfg.OS, DefaultOS)
	path := fmt.Sprintf("templates/%s.sh.tmpl", osName)
	data, err := templatesFS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("no built-in bootstrap template for os %q", osName)
	}
	return string(data), nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
