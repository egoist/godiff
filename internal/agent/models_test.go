package agent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestModelLists(t *testing.T) {
	codex, err := codexModels([]byte(`{"models":[
		{"slug":"gpt-b","display_name":"GPT-B","visibility":"list","priority":5},
		{"slug":"gpt-hidden","display_name":"Hidden","visibility":"hide","priority":1},
		{"slug":"gpt-a","display_name":"GPT-A","visibility":"list","priority":2}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := []Model{{ID: "gpt-a", Label: "GPT-A"}, {ID: "gpt-b", Label: "GPT-B"}}; !slices.Equal(codex, want) {
		t.Errorf("codex %+v", codex)
	}
	oc := providerModels([]byte("deepseek/deepseek-flash\nvercel/bfl/flux-3-image\n\nnot a model\n"))
	if want := []Model{{ID: "deepseek/deepseek-flash", Label: "deepseek-flash", Provider: "deepseek"}, {ID: "vercel/bfl/flux-3-image", Label: "bfl/flux-3-image", Provider: "vercel"}}; !slices.Equal(oc, want) {
		t.Errorf("opencode %+v", oc)
	}
	pi := piModels([]byte("provider   model             context\nanthropic  claude-opus-5-5   1M\nopenai     gpt-6-sol         400K\n"))
	if want := []Model{{ID: "anthropic/claude-opus-5-5", Label: "claude-opus-5-5", Provider: "anthropic"}, {ID: "openai/gpt-6-sol", Label: "gpt-6-sol", Provider: "openai"}}; !slices.Equal(pi, want) {
		t.Errorf("pi %+v", pi)
	}
	for raw, want := range map[string]string{
		`"openai/gpt-6"`: "openai/gpt-6",
		`{"providerID":"deepseek","model":"deepseek-flash","variant":"max"}`: "deepseek/deepseek-flash#max",
		`null`: "",
	} {
		if got := configModel([]byte(raw)); got != want {
			t.Errorf("configModel(%s) = %q, want %q", raw, got, want)
		}
	}
}

func TestOpenCodeDefault(t *testing.T) {
	t.Setenv("GODIFF_NO_SHELL_PATH", "1")
	dir := fakeAgent(t, "opencode", "")
	os.WriteFile(filepath.Join(dir, "cmd-models"), []byte("deepseek/deepseek-flash\nopenai/gpt-6\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "cmd---version"), []byte("opencode v2.0.22\n"), 0o644)
	// The config's model wins over the one used last.
	os.WriteFile(filepath.Join(dir, "cmd-debug"), []byte(`[{"type":"document","info":{"model":"openai/gpt-6"}},{"type":"directory"}]`), 0o644)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	os.MkdirAll(filepath.Join(state, "opencode"), 0o755)
	os.WriteFile(filepath.Join(state, "opencode", "model.json"), []byte(`{"recent":[{"providerID":"deepseek","modelID":"deepseek-flash"}],"variant":{"openai/gpt-6":"high"}}`), 0o644)
	a, _ := ByName("opencode")
	models, def, err := Models(context.Background(), a, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || def != "openai/gpt-6#high" {
		t.Errorf("models %+v, default %q", models, def)
	}
}
