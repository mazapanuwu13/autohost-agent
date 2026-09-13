package commands

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"autohost-agent/internal/domain"
)

// CommandKind distinguishes built-in commands from user-created shell scripts.
type CommandKind string

const (
	KindDefault CommandKind = "default"
	KindCustom  CommandKind = "custom"
)

// Command is the interface that all agent commands must implement.
type Command interface {
	// Execute runs the command with the given context and payload.
	Execute(ctx context.Context, payload map[string]any) error
}

// CommandWithOutput is an optional extension for built-in commands that
// produce output that must be captured in the job result (e.g. status checks).
type CommandWithOutput interface {
	Command
	ExecuteWithOutput(ctx context.Context, payload map[string]any) (string, error)
}

// registryEntry holds a command handler together with its kind.
type registryEntry struct {
	cmd  Command
	kind CommandKind
}

// CommandInfo is returned by ListCommands so callers know both name and kind.
type CommandInfo struct {
	Name string
	Kind CommandKind
}

// Registry maps job types to their command handlers.
type Registry struct {
	entries map[string]registryEntry
}

// NewRegistry creates a new command registry.
func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[string]registryEntry),
	}
}

// Register adds a built-in (default) command handler.
func (r *Registry) Register(jobType string, cmd Command) {
	r.entries[jobType] = registryEntry{cmd: cmd, kind: KindDefault}
}

// RegisterCustom adds a custom-script command handler.
func (r *Registry) RegisterCustom(jobType string, cmd Command) {
	r.entries[jobType] = registryEntry{cmd: cmd, kind: KindCustom}
}

// ExecuteResult holds the output and error from a command execution.
type ExecuteResult struct {
	Output string
	Err    error
}

// Execute finds and executes the command for the given job type.
// For CustomScriptCommands it captures the output; for built-ins output is "".
// If the command is not in the registry, it falls back to looking for a
// matching .sh script on disk (handles scripts created after agent startup).
func (r *Registry) Execute(ctx context.Context, jobType string, payload map[string]any) ExecuteResult {
	entry, ok := r.entries[jobType]
	if !ok {
		// container.* commands are handled by pattern — not in the registry.
		if strings.HasPrefix(jobType, "container.") {
			return execContainerCommand(ctx, jobType)
		}
		// Fallback: look for a custom script on disk
		return r.execFromDisk(ctx, jobType)
	}

	if custom, ok := entry.cmd.(*CustomScriptCommand); ok {
		output, err := custom.ExecuteWithOutput(ctx)
		return ExecuteResult{Output: output, Err: err}
	}

	// Built-in commands that need their output captured implement CommandWithOutput.
	if cwo, ok := entry.cmd.(CommandWithOutput); ok {
		output, err := cwo.ExecuteWithOutput(ctx, payload)
		return ExecuteResult{Output: output, Err: err}
	}

	err := entry.cmd.Execute(ctx, payload)
	return ExecuteResult{Err: err}
}

// customScriptPath returns the canonical path for a named custom script.
func customScriptPath(name string) string {
	return filepath.Join(domain.CustomCommandsDir, name+".sh")
}

// execFromDisk tries to find and run a custom script at the expected path.
// This handles commands that were registered after the agent started.
func (r *Registry) execFromDisk(ctx context.Context, name string) ExecuteResult {
	scriptPath := customScriptPath(name)
	cmd := &CustomScriptCommand{ScriptPath: scriptPath}
	output, err := cmd.ExecuteWithOutput(ctx)
	if err != nil {
		// If the script simply doesn't exist, give a clearer error
		return ExecuteResult{Output: output, Err: fmt.Errorf("command %q not found (looked for %s): %w", name, scriptPath, err)}
	}
	// Also register it so future invocations are faster
	r.RegisterCustom(name, cmd)
	return ExecuteResult{Output: output}
}

// ListCommands returns all registered command names with their kind.
func (r *Registry) ListCommands() []CommandInfo {
	out := make([]CommandInfo, 0, len(r.entries))
	for name, e := range r.entries {
		out = append(out, CommandInfo{Name: name, Kind: e.kind})
	}
	return out
}

// RegisterAll registers all built-in commands in the registry.
// Call this once during agent startup.
func RegisterAll(r *Registry) {
	r.Register("app.start", &AppStart{})
	r.Register("app.stop", &AppStop{})
	r.Register("app.restart", &AppRestart{})
	r.Register("app.remove", &AppRemove{})
	r.Register("docker.install", &DockerInstall{})
	r.Register("script.exec", &ScriptExec{})
	r.Register("marketplace.install", &MarketplaceInstall{})
	r.Register("caddy.upsert-route", &CaddyUpsertRoute{})
	r.Register("caddy.delete-route", &CaddyDeleteRoute{})
	r.Register("caddy.status", &CaddyStatus{})
	r.Register("docker.volume.backup", &DockerVolumeBackup{})
	r.Register("docker.volume.restore", &DockerVolumeRestore{})
	r.Register("docker.pg.backup", &DockerPgBackup{})
	r.Register("docker.pg.restore", &DockerPgRestore{})
	r.Register("swarm.init", &SwarmInitCommand{})
	r.Register("swarm.join", &SwarmJoinCommand{})
	r.Register("swarm.leave", &SwarmLeaveCommand{})
	r.Register("swarm.service.deploy", &SwarmServiceDeployCommand{})
	r.Register("swarm.service.scale", &SwarmServiceScaleCommand{})
	r.Register("swarm.service.remove", &SwarmServiceRemoveCommand{})
	r.Register("swarm.info", &SwarmInfoCommand{})
	r.Register("swarm.tasks.list", &SwarmTasksCommand{})
}
