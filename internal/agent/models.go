package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/godiff/internal/proc"
	"github.com/egoist/godiff/internal/shellenv"
)

// Model is a model an agent can review with.
type Model struct {
	// ID is what the agent's --model takes.
	ID string
	// Label names it among its provider's.
	Label string
	// Provider groups the models of the agents of many providers,
	// OpenCode's and Pi's; "" for the others.
	Provider string
}

// claudeModels are Claude Code's aliases of its latest models.
var claudeModels = []Model{
	{ID: "fable", Label: "Fable"},
	{ID: "opus", Label: "Opus"},
	{ID: "sonnet", Label: "Sonnet"},
	{ID: "haiku", Label: "Haiku"},
}

// Models lists the models an agent can review with, and the one it uses
// in dir when none is named, "" when the agent does not tell.
func Models(ctx context.Context, a Agent, dir string) (models []Model, def string, err error) {
	if a.Name == "claude" {
		return slices.Clone(claudeModels), "", nil
	}
	bin, err := shellenv.LookPath(a.Name)
	if err != nil {
		return nil, "", fmt.Errorf("%s is not installed", a.Label)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch a.Name {
	case "codex":
		out, err := output(ctx, bin, dir, "debug", "models")
		if err != nil {
			return nil, "", err
		}
		models, err = codexModels(out)
		return models, "", err
	case "opencode":
		out, err := output(ctx, bin, dir, "models")
		if err != nil {
			return nil, "", err
		}
		return providerModels(out), opencodeModel(ctx, bin, dir, "", opencodeMajor(ctx, bin) >= 2), nil
	case "pi":
		out, err := output(ctx, bin, dir, "--list-models")
		if err != nil {
			return nil, "", err
		}
		return piModels(out), "", nil
	}
	return nil, "", fmt.Errorf("unknown agent %s", a.Name)
}

// output runs a command of an agent in dir and returns what it prints.
func output(ctx context.Context, bin, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	proc.HideConsole(cmd)
	cmd.Dir = dir
	cmd.Env = shellenv.Environ()
	var stderr tail
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := lastLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s %s: %s", filepath.Base(bin), strings.Join(args, " "), msg)
		}
		return nil, fmt.Errorf("%s %s: %w", filepath.Base(bin), strings.Join(args, " "), err)
	}
	return out, nil
}

// codexModel is a model of Codex's catalog.
type codexModel struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	Visibility  string `json:"visibility"`
	Priority    int    `json:"priority"`
}

// codexModels reads Codex's catalog: the models it lists, in its order.
func codexModels(data []byte) ([]Model, error) {
	var catalog struct {
		Models []codexModel `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("could not read the models of Codex: %w", err)
	}
	listed := slices.DeleteFunc(catalog.Models, func(m codexModel) bool {
		return m.Slug == "" || (m.Visibility != "" && m.Visibility != "list")
	})
	slices.SortStableFunc(listed, func(a, b codexModel) int { return a.Priority - b.Priority })
	var models []Model
	for _, m := range listed {
		label := m.DisplayName
		if label == "" {
			label = m.Slug
		}
		models = append(models, Model{ID: m.Slug, Label: label})
	}
	return models, nil
}

// providerModels reads OpenCode's list of models, one provider/model a
// line.
func providerModels(data []byte) []Model {
	var models []Model
	for line := range strings.Lines(string(data)) {
		provider, name, ok := strings.Cut(strings.TrimSpace(line), "/")
		if ok && provider != "" && name != "" && !strings.ContainsAny(provider, " \t") {
			models = append(models, Model{ID: provider + "/" + name, Label: name, Provider: provider})
		}
	}
	return models
}

// piModels reads Pi's table of models, whose first columns are the
// provider and the model.
func piModels(data []byte) []Model {
	var models []Model
	for line := range strings.Lines(string(data)) {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "provider" {
			continue
		}
		models = append(models, Model{ID: f[0] + "/" + f[1], Label: f[1], Provider: f[0]})
	}
	return models
}

// opencodeMajor returns the major version of OpenCode, 1 when it does not
// tell.
func opencodeMajor(ctx context.Context, bin string) int {
	out, err := output(ctx, bin, "", "--version")
	f := strings.Fields(string(out))
	if err != nil || len(f) == 0 {
		return 1
	}
	v, _, _ := strings.Cut(strings.TrimPrefix(f[len(f)-1], "v"), ".")
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return 1
}

// opencodeModel is the model OpenCode reviews with in dir: the one chosen,
// else the one its own interface would start with, of its config or else
// used last. `opencode run` takes neither of these itself, and falls back
// on the first model it knows, which may not even write text. With
// variants, as from OpenCode 2, the variant chosen for the model in
// OpenCode comes with it. "" leaves the choice to OpenCode.
func opencodeModel(ctx context.Context, bin, dir, chosen string, variants bool) string {
	model := chosen
	if model == "" {
		model = opencodeConfigModel(ctx, bin, dir)
	}
	state := opencodeState()
	if model == "" && len(state.Recent) > 0 {
		r := state.Recent[0]
		if r.ProviderID != "" && r.ModelID != "" {
			model = r.ProviderID + "/" + r.ModelID
		}
	}
	if model == "" || !variants || strings.Contains(model, "#") {
		return model
	}
	if v := state.Variant[model]; v != "" && v != "default" {
		model += "#" + v
	}
	return model
}

// opencodeConfigModel is the model of OpenCode's config in dir, "" for
// none. OpenCode 2 lists the config's sources, the last winning; OpenCode
// 1, the config itself.
func opencodeConfigModel(ctx context.Context, bin, dir string) string {
	out, err := output(ctx, bin, dir, "debug", "config")
	if err != nil {
		return ""
	}
	out = bytes.TrimSpace(out)
	if bytes.HasPrefix(out, []byte("{")) {
		var conf struct {
			Model json.RawMessage `json:"model"`
		}
		json.Unmarshal(out, &conf)
		return configModel(conf.Model)
	}
	var sources []struct {
		Type string `json:"type"`
		Info struct {
			Model json.RawMessage `json:"model"`
		} `json:"info"`
	}
	json.Unmarshal(out, &sources)
	for i := len(sources) - 1; i >= 0; i-- {
		if s := sources[i]; s.Type == "document" {
			if m := configModel(s.Info.Model); m != "" {
				return m
			}
		}
	}
	return ""
}

// configModel reads a model of OpenCode's config: "provider/model", or an
// object with the provider, the model and its variant.
func configModel(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m struct {
		ProviderID string `json:"providerID"`
		Model      string `json:"model"`
		Variant    string `json:"variant"`
	}
	if json.Unmarshal(raw, &m) != nil || m.ProviderID == "" || m.Model == "" {
		return ""
	}
	id := m.ProviderID + "/" + m.Model
	if m.Variant != "" && m.Variant != "default" {
		id += "#" + m.Variant
	}
	return id
}

// opencodeStateFile is what OpenCode's interface remembers of the models:
// those used last, and the variant chosen for each.
type opencodeStateFile struct {
	Recent []struct {
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
	} `json:"recent"`
	Variant map[string]string `json:"variant"`
}

func opencodeState() opencodeStateFile {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	var st opencodeStateFile
	if data, err := os.ReadFile(filepath.Join(dir, "opencode", "model.json")); err == nil {
		json.Unmarshal(data, &st)
	}
	return st
}
