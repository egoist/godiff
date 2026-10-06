package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var files = []File{
	{Path: "src/greet.go", Status: 'M', Additions: 2, Deletions: 1, Patch: "@@ -1,3 +1,4 @@ func greet\n-\treturn \"Hello\"\n+\treturn \"Hi\"\n+\t// louder\n"},
	{Path: "src/greet_test.go", Status: 'A', Additions: 5, Patch: "@@ -0,0 +1,5 @@\n+package src\n"},
	{Path: "pnpm-lock.yaml", Status: 'M', Additions: 900, Deletions: 800, Generated: true, Patch: "@@ -1 +1 @@\n-a\n+b\n"},
	{Path: "logo.png", Status: 'A', Binary: true},
}

const answer = `{"overallSummary":"Greets louder.","groups":[
 {"key":"greeting","label":"Greeting","summary":"Shorter greeting.","critical":false,
  "filePaths":["src/greet.go","./src/greet_test.go","invented.go"],
  "fileNotes":[{"path":"src/greet.go","text":"Changes the public output.","critical":true},{"path":"nope.go","text":"x","critical":false}],
  "lineNotes":[{"path":"src/greet.go","side":"additions","line":2,"text":"Callers may match on Hello.","critical":false},{"path":"src/greet.go","side":"sideways","line":2,"text":"x","critical":false}]},
 {"key":"deps","label":"Lockfile","summary":"","critical":false,"filePaths":["pnpm-lock.yaml","src/greet.go"],"fileNotes":[],"lineNotes":[]}
]}`

func TestParse(t *testing.T) {
	a, err := parse("Here it is:\n```json\n"+answer+"\n```\nDone.", files)
	if err != nil {
		t.Fatal(err)
	}
	if a.OverallSummary != "Greets louder." || len(a.Groups) != 3 {
		t.Fatalf("analysis %+v", a)
	}
	g := a.Groups[0]
	if !slices.Equal(g.FilePaths, []string{"src/greet.go", "src/greet_test.go"}) || len(g.FileNotes) != 1 || len(g.LineNotes) != 1 {
		t.Errorf("first group %+v", g)
	}
	// A path in two groups stays in the first.
	if !slices.Equal(a.Groups[1].FilePaths, []string{"pnpm-lock.yaml"}) {
		t.Errorf("second group %+v", a.Groups[1])
	}
	// The files left out gather at the end.
	if last := a.Groups[2]; last.Key != "other-changes" || !slices.Equal(last.FilePaths, []string{"logo.png"}) {
		t.Errorf("last group %+v", last)
	}
	if a.Critical() != 1 {
		t.Errorf("critical %d", a.Critical())
	}
	if _, err := parse("I could not do it.", files); err == nil {
		t.Error("no error for an answer without JSON")
	}
}

func TestPrompt(t *testing.T) {
	req := &Request{Title: "Greet", Description: "Shorter.", Commits: []string{"a1 First", "b2 Second"}, Files: files, Rev: "abc123"}
	user := userPrompt(req, true)
	for _, want := range []string{"Title: Greet", "---DESCRIPTION---\nShorter.", "---COMMITS--- (2, oldest first)", "src/\n  greet.go  M  +2/-1", "pnpm-lock.yaml  M  +900/-800  [generated]", "### src/greet.go [M, +2/-1]\n@@ -1,3 +1,4 @@", "(generated file, diff omitted)", "(binary file, no diff shown)"} {
		if !strings.Contains(user, want) {
			t.Errorf("no %q in\n%s", want, user)
		}
	}
	sys := systemPrompt(req, "", true)
	if !strings.Contains(sys, "git show abc123:<path>") || !strings.Contains(sys, "fenced ```json block") || !strings.Contains(sys, `"lineNotes"`) {
		t.Errorf("system prompt:\n%s", sys)
	}
	var schemaDoc map[string]any
	if err := json.Unmarshal([]byte(schema), &schemaDoc); err != nil {
		t.Fatalf("schema: %v", err)
	}
}

// fakeAgent installs a command of an agent's name that records how it
// ran and prints out, as the agent's stream would.
func fakeAgent(t *testing.T, name, out string) string {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agents are shell scripts")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "out"), []byte(out), 0o644)
	script := `#!/bin/sh
dir=$(dirname "$0")
printf '%s\n' "$@" > "$dir/args"
cat > "$dir/stdin"
last=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then last="$2"; fi
  shift
done
if [ -n "$last" ] && [ -f "$dir/last" ]; then cp "$dir/last" "$last"; fi
cat "$dir/out"
`
	os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func jsonLine(v any) string {
	data, _ := json.Marshal(v)
	return string(data) + "\n"
}

func TestRun(t *testing.T) {
	t.Setenv("GODIFF_NO_SHELL_PATH", "1")
	var structured map[string]any
	json.Unmarshal([]byte(answer), &structured)
	fenced := "```json\n" + answer + "\n```"
	for _, tc := range []struct {
		agent    string
		out      string
		last     string // what codex writes to its -o file
		model    string
		args     []string
		progress string
	}{
		{
			agent: "claude",
			out: jsonLine(map[string]any{"type": "system", "subtype": "init", "model": "claude-test"}) +
				jsonLine(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"file_path": "/repo/src/greet.go"}}}}}) +
				jsonLine(map[string]any{"type": "result", "is_error": false, "result": "", "structured_output": structured}),
			model:    "claude-test",
			args:     []string{"-p", "--json-schema", "--permission-mode", "dontAsk", "Bash(git show:*)"},
			progress: "Reading greet.go",
		},
		{
			agent: "codex",
			out: jsonLine(map[string]any{"type": "item.started", "item": map[string]any{"type": "command_execution", "command": `/bin/zsh -lc "git show abc:src/greet.go"`}}) +
				jsonLine(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": answer}}),
			last:     answer,
			args:     []string{"exec", "--sandbox", "read-only", "--output-schema", "-"},
			progress: "Running git show abc:src/greet.go",
		},
		{
			agent: "opencode",
			out: jsonLine(map[string]any{"type": "tool_use", "part": map[string]any{"type": "tool", "tool": "grep", "state": map[string]any{"input": map[string]any{"pattern": "Hello"}}}}) +
				jsonLine(map[string]any{"type": "text", "part": map[string]any{"type": "text", "text": fenced}}),
			args:     []string{"run", "--format", "json"},
			progress: `Searching for "Hello"`,
		},
		{
			agent: "pi",
			out: jsonLine(map[string]any{"type": "tool_execution_start", "toolName": "read", "args": map[string]any{"path": "src/greet.go"}}) +
				jsonLine(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "model": "pi-model", "content": []any{map[string]any{"type": "text", "text": fenced}}, "stopReason": "stop"}}),
			model:    "pi-model",
			args:     []string{"-p", "--mode", "json", "--tools", "read,grep,find,ls", "--append-system-prompt"},
			progress: "Reading greet.go",
		},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			dir := fakeAgent(t, tc.agent, tc.out)
			if tc.last != "" {
				os.WriteFile(filepath.Join(dir, "last"), []byte(tc.last), 0o644)
			}
			a, _ := ByName(tc.agent)
			var steps []string
			an, err := Run(context.Background(), a, Request{Title: "Greet", Files: files, Dir: t.TempDir(), Rev: "abc"}, func(s string) { steps = append(steps, s) })
			if err != nil {
				t.Fatal(err)
			}
			if len(an.Groups) != 3 || an.Agent != tc.agent || an.Model != tc.model || an.GeneratedAt.IsZero() {
				t.Errorf("analysis %+v", an)
			}
			if !slices.Contains(steps, tc.progress) {
				t.Errorf("progress %q, want %q", steps, tc.progress)
			}
			args, _ := os.ReadFile(filepath.Join(dir, "args"))
			for _, want := range tc.args {
				if !slices.Contains(strings.Split(string(args), "\n"), want) {
					t.Errorf("no %q in the arguments:\n%s", want, args)
				}
			}
			stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
			if !strings.Contains(string(stdin), "---MANIFEST---") {
				t.Errorf("stdin:\n%s", stdin)
			}
		})
	}
}

func TestRunErrors(t *testing.T) {
	t.Setenv("GODIFF_NO_SHELL_PATH", "1")
	fakeAgent(t, "codex", jsonLine(map[string]any{"type": "turn.failed", "error": map[string]any{"message": `{"type":"error","status":400,"error":{"message":"The model is not supported."}}`}}))
	a, _ := ByName("codex")
	_, err := Run(context.Background(), a, Request{Files: files, Dir: t.TempDir()}, nil)
	if err == nil || err.Error() != "Codex failed: The model is not supported." {
		t.Errorf("error %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	a, _ = ByName("opencode")
	if _, err := Run(context.Background(), a, Request{Files: files, Dir: t.TempDir()}, nil); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error %v", err)
	}
}
