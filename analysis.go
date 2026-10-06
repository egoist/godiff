package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/godiff/internal/agent"
	"github.com/egoist/mygo/ui"
)

// analysisState is an agent's review of a source: the one it gave, or the
// one under way.
type analysisState struct {
	result *agent.Analysis
	// fingerprints are the files' as the agent saw them, to tell which
	// changed since.
	fingerprints map[string]string
	running      bool
	progress     string
	started      time.Time
	err          string
	cancel       context.CancelFunc
	agent        string // the label of the agent at work
	folded       bool
	// offered is set when the user chose to group by a review there is
	// not yet: the card offers one, which runs once they ask.
	offered bool
	// checked is set once a review saved was looked for.
	checked bool
}

// runAgent runs an agent; tests replace it.
var runAgent = agent.Run

func (w *window) analysisFor(src source) *analysisState {
	if w.analyses == nil {
		w.analyses = map[source]*analysisState{}
	}
	st := w.analyses[src]
	if st == nil {
		st = &analysisState{}
		w.analyses[src] = st
	}
	return st
}

// analysisResult is the agent's review of the source shown, nil for none.
func (w *window) analysisResult() *agent.Analysis {
	if st := w.analyses[w.source]; st != nil {
		return st.result
	}
	return nil
}

// showSummary reports whether the surface shows the agent's review: once
// there is one, one under way, one that failed, or one offered.
func (w *window) showSummary() bool {
	st := w.analyses[w.source]
	return st != nil && (st.result != nil || st.running || st.err != "" || st.offered)
}

// analysisKey names a source for the reviews saved.
func analysisKey(root string, src source) string {
	kind := map[sourceKind]string{sourceWorkingTree: "worktree", sourceCommit: "commit", sourceBranch: "branch", sourcePull: "pull"}[src.kind]
	sum := sha1.Sum([]byte(root + "\x00" + kind + ":" + src.ref))
	return hex.EncodeToString(sum[:])
}

// analysisPath is where the review of a source is saved, "" without a
// place for it, as in tests.
func analysisPath(root string, src source) string {
	if state.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(state.path), "analyses", analysisKey(root, src)+".json")
}

type savedAnalysis struct {
	Analysis     *agent.Analysis   `json:"analysis"`
	Fingerprints map[string]string `json:"fingerprints"`
}

// loadAnalysis reads the review saved of a source, once.
func (w *window) loadAnalysis(src source) {
	st := w.analysisFor(src)
	if st.checked {
		return
	}
	st.checked = true
	path := analysisPath(w.repo.Root, src)
	if path == "" {
		return
	}
	w.background(func() {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		var saved savedAnalysis
		if json.Unmarshal(data, &saved) != nil || saved.Analysis == nil || len(saved.Analysis.Groups) == 0 {
			return
		}
		w.update(func() {
			if st.result != nil || st.running {
				return
			}
			st.result, st.fingerprints = saved.Analysis, saved.Fingerprints
			st.folded = true
			if w.source == src {
				w.regroupSoon()
			}
		})
	})
}

func saveAnalysis(path string, a *agent.Analysis, fingerprints map[string]string) {
	if path == "" {
		return
	}
	data, err := json.Marshal(savedAnalysis{Analysis: a, Fingerprints: fingerprints})
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, data, 0o644)
}

// runAnalysis asks the agent of the settings, else the first installed, to
// review the changes shown.
func (w *window) runAnalysis() {
	src := w.source
	st := w.analysisFor(src)
	if st.running || len(w.files) == 0 || w.loading {
		return
	}
	req := agent.Request{Dir: w.repo.Root, Model: w.settings.AIModel}
	var base, head string
	switch src.kind {
	case sourceWorkingTree:
		req.Title, req.Checkout = "Uncommitted changes", true
		if w.branch != "" {
			req.Title += " on " + w.branch
		}
	case sourceBranch:
		req.Title, req.Checkout = fmt.Sprintf("The changes of %s since it branched from %s", w.branch, src.ref), true
		base, head = w.base, "HEAD"
	case sourceCommit:
		if w.commit != nil {
			req.Title, req.Description = w.commit.Subject, w.commit.Body
		}
		req.Rev = src.ref
	case sourcePull:
		if w.pr == nil || w.pr.meta == nil {
			return
		}
		m := w.pr.meta
		req.Title, req.Description, req.URL, req.Rev = m.Title, plainBody(m.Body), m.HTMLURL, w.pr.head
		base, head = w.base, w.pr.head
	}
	files := slices.Clone(w.files)
	fingerprints := map[string]string{}
	for _, f := range files {
		fingerprints[f.Path] = f.Fingerprint
	}
	name := w.settings.AIAgent
	path := analysisPath(w.repo.Root, src)
	ctx, cancel := context.WithCancel(context.Background())
	st.running, st.err, st.progress, st.started, st.cancel, st.folded, st.offered = true, "", "Starting…", time.Now(), cancel, false, false
	w.rowsDirty = true
	w.list.ScrollTo(0, ui.Start)
	w.background(func() {
		defer cancel()
		a, ok := agent.ByName(name)
		if !ok {
			installed := agent.Installed()
			if len(installed) == 0 {
				w.update(func() {
					st.running = false
					st.err = "No coding agent was found. Install Claude Code, Codex, OpenCode or Pi, or name one as aiAgent in the config file."
					w.rowsDirty = true
				})
				return
			}
			a = installed[0]
		}
		w.update(func() { st.agent = a.Label })
		// The files' descriptions read no state the main thread changes.
		req.Files = analysisFiles(files)
		if base != "" && head != "" {
			req.Commits = w.repo.Subjects(base, head, 100)
		}
		last := ""
		an, err := runAgent(ctx, a, req, func(s string) {
			if s == last {
				return
			}
			last = s
			w.update(func() { st.progress = s })
		})
		if err == nil {
			saveAnalysis(path, an, fingerprints)
		}
		w.update(func() {
			st.running, st.cancel = false, nil
			switch {
			case errors.Is(err, context.Canceled):
				st.err = ""
			case err != nil:
				st.err = err.Error()
			default:
				st.result, st.fingerprints, st.err = an, fingerprints, ""
			}
			if w.source == src {
				w.regroupSoon()
			}
		})
	})
}

// stopAnalysis stops the agent at work on the source shown.
func (w *window) stopAnalysis() {
	if st := w.analyses[w.source]; st != nil && st.cancel != nil {
		st.cancel()
	}
}

// changedSince counts the files that changed since the agent's review: new,
// gone or different.
func (w *window) changedSince(st *analysisState) int {
	n := 0
	seen := map[string]bool{}
	for _, f := range w.files {
		seen[f.Path] = true
		if fp, ok := st.fingerprints[f.Path]; !ok || fp != f.Fingerprint {
			n++
		}
	}
	for p := range st.fingerprints {
		if !seen[p] {
			n++
		}
	}
	return n
}

// summaryCard shows the agent's review of the changes: its summary, or
// what it does while at work.
func (w *window) summaryCard(c *ui.Context, pal *palette) *ui.Element {
	t := c.Theme()
	st := w.analyses[w.source]
	return ui.Column(c).Padding(12, 16).Gap(8).Radius(cardRadius).Background(pal.headerBg).Border(1, t.Accent.Alpha(0.3)).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Icon(c, iconSparkle).FontSize(15).TextColor(t.Accent)
			ui.Text(c, "AI Review").FontSize(14).Bold().Shrink(0)
			if a := st.result; a != nil && !st.running {
				by := a.Agent
				if ag, ok := agent.ByName(a.Agent); ok {
					by = ag.Label
				}
				if a.Model != "" {
					by += " · " + a.Model
				}
				by += " · " + relativeTime(w.now, a.GeneratedAt)
				ui.Text(c, by).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
			}
			ui.Spacer(c)
			if st.result == nil && !st.running && st.err == "" {
				// Offered: nothing runs until asked.
				w.agentMenu(c)
				if ui.PrimaryButton(c, "Review with AI").Height(26).Clicked() {
					w.runAnalysis()
				}
				if iconButton(c, iconClose, "Dismiss").Size(26, 26).Clicked() {
					st.offered = false
					if w.groupPref == groupAI {
						w.groupPref = groupAuto
					}
					w.regroupSoon()
				}
				return
			}
			if st.running {
				if ui.Button(c, "").Height(26).Children(func() {
					ui.Icon(c, iconStop).FontSize(12)
					ui.Text(c, "Stop")
				}).Clicked() {
					w.stopAnalysis()
				}
				return
			}
			w.agentMenu(c)
			label := "Review Again"
			if st.result == nil {
				label = "Try Again"
			}
			if ui.Button(c, label).Height(26).Clicked() {
				w.runAnalysis()
			}
			if st.result != nil {
				tip := "Hide the summary"
				if st.folded {
					tip = "Show the summary"
				}
				b := iconButton(c, iconChevronDown, tip).Size(26, 26)
				if b.Clicked() {
					st.folded = !st.folded
				}
			}
		})
		if st.running {
			ui.Row(c).Gap(8).Children(func() {
				ui.Spinner(c).Size(14, 14)
				who := st.agent
				if who == "" {
					who = "The agent"
				}
				ui.Text(c, who+": "+st.progress).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1).Shrink(1).MinWidth(0)
				elapsed := w.now.Sub(st.started).Truncate(time.Second)
				ui.Text(c, elapsed.String()).Font(w.codeFont()).FontSize(11).TextColor(t.TextMuted).Shrink(0)
				c.After(time.Second)
			})
		}
		if st.result == nil && !st.running && st.err == "" {
			ui.Text(c, "A coding agent on this machine can summarize these changes, group the files by intent and note what deserves attention. It reads, never writes, and starts only when you ask.").
				FontSize(13).TextColor(t.TextMuted)
		}
		if st.err != "" {
			ui.Text(c, st.err).FontSize(12).TextColor(t.Danger).Selectable().MaxLines(6)
		}
		a := st.result
		if a == nil || st.folded || st.running {
			return
		}
		if s := strings.TrimSpace(a.OverallSummary); s != "" {
			ui.Text(c, s).FontSize(13).Selectable()
		}
		notes := 0
		for _, g := range a.Groups {
			notes += len(g.FileNotes) + len(g.LineNotes)
		}
		ui.Row(c).Gap(10).Children(func() {
			ui.Textf(c, "%s · %s", plural(len(a.Groups), "group"), plural(notes, "note")).FontSize(12).TextColor(t.TextMuted)
			if n := a.Critical(); n > 0 {
				ui.Row(c).Gap(4).Children(func() {
					ui.Icon(c, iconAlert).FontSize(12).TextColor(t.Danger)
					ui.Textf(c, "%d to take care with", n).FontSize(12).FontWeight(600).TextColor(t.Danger)
				})
			}
			if n := w.changedSince(st); n > 0 {
				ui.Textf(c, "%s changed since", plural(n, "file")).FontSize(12).TextColor(pal.ref).
					Tooltip("Notes on the files changed since do not show. Review again to include them.")
			}
			ui.Spacer(c)
			if w.grouping() != groupAI {
				b := ui.ButtonBase(c).Children(func() {
					ui.Text(c, "Group files by this review").FontSize(12).FontWeight(600).TextColor(t.Accent)
				})
				if b.Clicked() {
					w.setGrouping(groupAI)
				}
			}
		})
	})
}

// agentMenu chooses the agent that reviews.
func (w *window) agentMenu(c *ui.Context) {
	installed, known := installedAgents()
	label := "Automatic"
	if a, ok := agent.ByName(w.settings.AIAgent); ok {
		label = a.Label
	}
	ui.MenuButton(c, label, func(m *ui.Menu) {
		if m.Item("Automatic").Checked(w.settings.AIAgent == "").Chosen() {
			cfg.Update(func(s *Settings) { s.AIAgent = "" })
		}
		m.Separator()
		for _, a := range agent.All {
			missing := known && !slices.Contains(installed, a)
			item := a.Label
			if missing {
				item += " (not installed)"
			}
			if m.Item(item).Checked(w.settings.AIAgent == a.Name).Disabled(missing).Chosen() {
				name := a.Name
				cfg.Update(func(s *Settings) { s.AIAgent, s.AIModel = name, "" })
			}
		}
	}).Height(26).Tooltip("The agent that reviews")
}

// detectAgents is set by the app: tests look for no agents.
var detectAgents bool

var agentsFound struct {
	sync.Mutex
	list          []agent.Agent
	started, done bool
}

// installedAgents returns the agents installed, once known: they are
// looked for once, off the main thread.
func installedAgents() ([]agent.Agent, bool) {
	agentsFound.Lock()
	defer agentsFound.Unlock()
	if !agentsFound.started && detectAgents {
		agentsFound.started = true
		go func() {
			list := agent.Installed()
			agentsFound.Lock()
			agentsFound.list, agentsFound.done = list, true
			agentsFound.Unlock()
			windowsMu.Lock()
			for _, w := range windows {
				w.invalidate()
			}
			windowsMu.Unlock()
		}()
	}
	return agentsFound.list, agentsFound.done
}

// groupControl chooses how the files group: not at all, by kind, or by
// the agent's review, which it asks for when there is none.
func (w *window) groupControl(c *ui.Context, pal *palette) {
	t := c.Theme()
	modes := []groupMode{groupNone, groupKind, groupAI}
	current := slices.Index(modes, w.grouping())
	st := w.analyses[w.source]
	running := st != nil && st.running
	choice := current
	seg := ui.SegmentedBase(c, &choice, len(modes))
	seg.Track.Padding(2).Gap(2).Radius(8).Background(ui.RGBA(127, 127, 127, 0.1)).Label("Group files").Children(func() {
		for i, it := range []struct {
			icon *ui.SVG
			name string
		}{{iconList, "No groups"}, {iconLayers, "Group by kind"}, {iconSparkle, "Group by an AI review"}} {
			s := seg.Segment(i).Size(30, 24).Radius(6).Center().Label(it.name).Tooltip(it.name).TextColor(t.TextMuted)
			if i == current {
				s.Background(pal.headerBg).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.12)).TextColor(t.Text)
			}
			s.Children(func() {
				ic := ui.Icon(c, it.icon).FontSize(15)
				if i == 2 && running {
					ic.TextColor(t.Accent)
					ic.Opacity(0.4 + 0.6*ic.Loop("pulse", 1200*time.Millisecond, ui.Bounce(ui.EaseInOut)))
				}
			})
		}
	})
	if choice != current {
		w.chooseGrouping(modes[choice])
	}
}

// chooseGrouping groups the files as the user chose. Grouping by an AI
// review there is not yet offers one: an agent runs only when asked.
func (w *window) chooseGrouping(mode groupMode) {
	if mode == groupAI && w.analysisResult() == nil {
		w.groupPref = groupAI
		st := w.analysisFor(w.source)
		if !st.running {
			st.offered, st.err = true, ""
		}
		w.rowsDirty = true
		w.list.ScrollTo(0, ui.Start)
		return
	}
	w.setGrouping(mode)
}

// reviewWithAI asks the agent for a review, or shows the one there is.
func (w *window) reviewWithAI() {
	st := w.analyses[w.source]
	if st != nil && (st.result != nil || st.running) {
		st.folded = false
		w.rowsDirty = true
		w.list.ScrollTo(0, ui.Start)
		return
	}
	w.runAnalysis()
}
