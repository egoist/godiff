package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/godiff/internal/proc"
	"github.com/egoist/godiff/internal/shellenv"
)

// timeout bounds a run: the agents cap their own turns, and the user can
// stop one sooner.
const timeout = 20 * time.Minute

// Run asks an agent to review a change, reporting what it does as it goes,
// and returns its analysis.
func Run(ctx context.Context, a Agent, req Request, progress func(string)) (*Analysis, error) {
	if progress == nil {
		progress = func(string) {}
	}
	bin, err := shellenv.LookPath(a.Name)
	if err != nil {
		return nil, fmt.Errorf("%s is not installed: %s was not found in your PATH", a.Label, a.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Diffs too large for the prompt go to a file the agent reads, in the
	// repository's git directory, which the agents may read.
	inline := len(diffs(req.Files)) <= inlineLimit
	patchPath := ""
	if !inline {
		dir := filepath.Join(req.Dir, ".git")
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			dir = os.TempDir()
		}
		f, err := os.CreateTemp(dir, "godiff-review-*.patch")
		if err != nil {
			return nil, err
		}
		patchPath = f.Name()
		f.WriteString(diffs(req.Files))
		f.Close()
		defer os.Remove(patchPath)
	}

	r := &runner{agent: a, req: &req, progress: progress, model: req.Model}
	system := systemPrompt(&req, patchPath, a.Name != "claude" && a.Name != "codex")
	user := userPrompt(&req, inline)
	var args, env []string
	stdin := system + "\n\n" + user
	switch a.Name {
	case "claude":
		stdin = user
		args = []string{"-p", "--output-format", "stream-json", "--verbose",
			"--system-prompt", system, "--json-schema", schema,
			"--permission-mode", "dontAsk", "--no-session-persistence", "--strict-mcp-config",
			"--allowedTools", "Read", "Grep", "Glob", "Bash(git show:*)", "Bash(git log:*)", "Bash(git diff:*)",
			"--disallowedTools", "Edit", "Write", "NotebookEdit", "WebFetch", "WebSearch", "Task"}
		if patchPath != "" {
			args = append(args, "--add-dir", filepath.Dir(patchPath))
		}
		if req.Model != "" {
			args = append(args, "--model", req.Model)
		}
	case "codex":
		schemaFile, err := os.CreateTemp("", "godiff-schema-*.json")
		if err != nil {
			return nil, err
		}
		schemaFile.WriteString(schema)
		schemaFile.Close()
		defer os.Remove(schemaFile.Name())
		last, err := os.CreateTemp("", "godiff-answer-*.txt")
		if err != nil {
			return nil, err
		}
		last.Close()
		defer os.Remove(last.Name())
		r.lastFile = last.Name()
		args = []string{"exec", "--json", "--sandbox", "read-only", "--skip-git-repo-check", "--ephemeral",
			"--color", "never", "--output-schema", schemaFile.Name(), "-o", last.Name(), "-C", req.Dir}
		if req.Model != "" {
			args = append(args, "-m", req.Model)
		}
		args = append(args, "-")
	case "opencode":
		// The default agent with the tools that change anything denied:
		// the plan agent will not read files.
		env = append(env, `OPENCODE_CONFIG_CONTENT={"permission":{"edit":"deny","bash":"deny","webfetch":"deny"}}`)
		args = []string{"run", "--format", "json"}
		if req.Model != "" {
			args = append(args, "--model", req.Model)
		}
	case "pi":
		stdin = user
		args = []string{"-p", "--mode", "json", "--no-session", "--tools", "read,grep,find,ls", "--append-system-prompt", system}
		if req.Model != "" {
			args = append(args, "--model", req.Model)
		}
	default:
		return nil, fmt.Errorf("unknown agent %s", a.Name)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	proc.HideConsole(cmd)
	proc.KillGroup(cmd)
	cmd.Dir = req.Dir
	cmd.Env = append(shellenv.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr tail
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	progress("Starting " + a.Label + "…")
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start %s: %w", a.Label, err)
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		r.line(sc.Bytes())
	}
	io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s took over %v", a.Label, timeout)
		}
		return nil, context.Canceled
	}
	if r.lastFile != "" {
		if data, err := os.ReadFile(r.lastFile); err == nil && len(bytes.TrimSpace(data)) > 0 {
			r.final = string(data)
		}
	}
	if r.structured == nil && strings.TrimSpace(r.final) == "" {
		msg := r.err
		if msg == "" {
			msg = lastLine(stderr.String())
		}
		if msg == "" && waitErr != nil {
			msg = waitErr.Error()
		}
		if msg == "" {
			msg = "it answered nothing"
		}
		return nil, fmt.Errorf("%s failed: %s", a.Label, msg)
	}
	progress("Organizing the review…")
	var an *Analysis
	if r.structured != nil {
		var parsed Analysis
		if err = json.Unmarshal(r.structured, &parsed); err == nil {
			an, err = fit(&parsed, req.Files)
		}
	} else {
		an, err = parse(r.final, req.Files)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.Label, err)
	}
	an.Agent = a.Name
	an.Model = r.model
	an.GeneratedAt = time.Now()
	return an, nil
}

// runner reads an agent's stream of JSON lines.
type runner struct {
	agent    Agent
	req      *Request
	progress func(string)
	model    string
	// The answer: its text, or the object an agent validated itself.
	final      string
	structured json.RawMessage
	lastFile   string
	err        string
}

func (r *runner) line(data []byte) {
	if len(bytes.TrimSpace(data)) == 0 || data[0] != '{' {
		return
	}
	switch r.agent.Name {
	case "claude":
		r.claude(data)
	case "codex":
		r.codex(data)
	case "opencode":
		r.opencode(data)
	case "pi":
		r.pi(data)
	}
}

func (r *runner) claude(data []byte) {
	var ev struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Model   string `json:"model"`
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
		IsError    bool            `json:"is_error"`
		Result     string          `json:"result"`
		Structured json.RawMessage `json:"structured_output"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" && ev.Model != "" {
			r.model = ev.Model
		}
	case "assistant":
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "tool_use":
				if c.Name != "StructuredOutput" {
					r.progress(toolStep(c.Name, c.Input))
				}
			case "text":
				r.progress("Thinking…")
			case "thinking":
				r.progress("Thinking…")
			}
		}
	case "result":
		if ev.IsError {
			r.err = ev.Result
			return
		}
		if len(ev.Structured) > 0 && string(ev.Structured) != "null" {
			r.structured = ev.Structured
		}
		r.final = ev.Result
	}
}

func (r *runner) codex(data []byte) {
	var ev struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
		Item struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Command string `json:"command"`
		} `json:"item"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	switch ev.Type {
	case "item.started":
		if ev.Item.Type == "command_execution" {
			r.progress("Running " + shortCommand(ev.Item.Command))
		}
	case "item.completed":
		switch ev.Item.Type {
		case "agent_message":
			r.final = ev.Item.Text
		case "reasoning":
			r.progress("Thinking…")
		}
	case "error":
		r.err = errorMessage(ev.Message)
	case "turn.failed":
		r.err = errorMessage(ev.Error.Message)
	}
}

func (r *runner) opencode(data []byte) {
	var ev struct {
		Type  string `json:"type"`
		Error struct {
			Message string `json:"message"`
			Data    struct {
				Message string `json:"message"`
			} `json:"data"`
		} `json:"error"`
		Part struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Tool  string `json:"tool"`
			State struct {
				Input json.RawMessage `json:"input"`
			} `json:"state"`
		} `json:"part"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	switch ev.Type {
	case "tool_use":
		r.progress(toolStep(ev.Part.Tool, ev.Part.State.Input))
	case "text":
		r.final = ev.Part.Text
		r.progress("Thinking…")
	case "error":
		r.err = ev.Error.Message
		if r.err == "" {
			r.err = ev.Error.Data.Message
		}
	}
}

func (r *runner) pi(data []byte) {
	var ev struct {
		Type     string          `json:"type"`
		ToolName string          `json:"toolName"`
		Args     json.RawMessage `json:"args"`
		Message  struct {
			Role    string `json:"role"`
			Model   string `json:"model"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StopReason   string `json:"stopReason"`
			ErrorMessage string `json:"errorMessage"`
		} `json:"message"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return
	}
	switch ev.Type {
	case "tool_execution_start":
		r.progress(toolStep(ev.ToolName, ev.Args))
	case "message_end":
		m := ev.Message
		if m.Role != "assistant" {
			return
		}
		if m.Model != "" {
			r.model = m.Model
		}
		if m.StopReason == "error" {
			r.err = firstLine(m.ErrorMessage)
			return
		}
		var text strings.Builder
		for _, c := range m.Content {
			if c.Type == "text" {
				text.WriteString(c.Text)
			}
		}
		if text.Len() > 0 {
			r.final = text.String()
		}
		r.progress("Thinking…")
	}
}

// toolStep describes a tool call: Reading main.go, Searching for "foo".
func toolStep(tool string, input json.RawMessage) string {
	var in map[string]any
	json.Unmarshal(input, &in)
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := in[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	path := str("file_path", "filePath", "path")
	if path != "" {
		path = filepath.Base(path)
	}
	switch strings.ToLower(tool) {
	case "read", "view":
		if path != "" {
			return "Reading " + path
		}
		return "Reading files"
	case "grep", "search":
		if p := str("pattern", "query"); p != "" {
			return fmt.Sprintf("Searching for %q", p)
		}
		return "Searching"
	case "glob", "find", "ls", "list":
		return "Looking through the files"
	case "bash", "shell", "exec":
		if c := str("command", "cmd"); c != "" {
			return "Running " + shortCommand(c)
		}
	}
	return "Using " + tool
}

// shortCommand is a command as its first line, without the shell codex
// wraps it in.
func shortCommand(c string) string {
	for _, p := range []string{"/bin/zsh -lc ", "/bin/bash -lc ", "bash -lc ", "/bin/sh -c "} {
		c = strings.TrimPrefix(c, p)
	}
	c = strings.Trim(firstLine(c), `"'`)
	if len(c) > 60 {
		c = c[:60] + "…"
	}
	return c
}

// errorMessage reads the message of an error that holds an API's answer as
// JSON, as codex's do.
func errorMessage(s string) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(s), &body) == nil {
		if body.Error.Message != "" {
			return body.Error.Message
		}
		if body.Message != "" {
			return body.Message
		}
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// lastLine is the last line of a command's output that says something.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && !strings.HasPrefix(l, "Warning:") {
			return l
		}
	}
	return ""
}

// tail keeps the end of what a command writes.
type tail struct{ b []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 16<<10 {
		t.b = t.b[len(t.b)-(8<<10):]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.b) }
