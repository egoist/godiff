package github

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/egoist/godiff/internal/proc"
	"github.com/egoist/godiff/internal/shellenv"
)

// FindToken returns a token for github.com from the environment, else from
// the GitHub CLI's login, "" when there is neither.
func FindToken() string {
	for _, k := range []string{"GODIFF_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	gh, err := shellenv.LookPath("gh")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "auth", "token", "--hostname", "github.com")
	proc.HideConsole(cmd)
	cmd.Env = shellenv.Environ()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
