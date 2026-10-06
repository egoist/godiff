package main

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/godiff/internal/agent"
	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/godiff/internal/git"
	"github.com/egoist/mygo/ui"
)

// groupMode is how the review groups its files, as pulls.review does: by
// their kind, or by intent as an agent's review has them.
type groupMode uint8

const (
	groupAuto groupMode = iota // by the agent's review once there is one, else by kind for pull requests
	groupNone
	groupKind
	groupAI
)

// fileGroup is a group of the files, which show together.
type fileGroup struct {
	key, label string
	summary    string
	critical   bool
	ai         bool
}

// kind is a kind of file, as pulls.review's rules sort them.
type kind struct{ key, label, summary string }

// kinds are the kinds of files, in the order their groups show.
var kinds = []kind{
	{"docs", "Docs", "Documentation updates."},
	{"code", "Code", "Source changes."},
	{"tests", "Tests", "Test coverage for the change."},
	{"config", "Config", "Configuration changes."},
	{"deps", "Dependencies", "Dependency version changes."},
	{"generated", "Generated", "Generated or build output changes."},
	{"other", "Other", "Other changes that don't fit an existing category."},
}

// kindOf returns the index in kinds of a file's kind. The first rule that
// matches wins: a document under .github is a document.
func kindOf(f *diff.File) int {
	index := func(key string) int {
		return slices.IndexFunc(kinds, func(k kind) bool { return k.key == key })
	}
	if f.Directory {
		return index("generated")
	}
	if f.Binary {
		return index("other")
	}
	p := strings.ToLower(f.Path)
	parts := strings.Split(p, "/")
	base, dirs := parts[len(parts)-1], parts[:len(parts)-1]
	inDir := func(names ...string) bool {
		for _, d := range dirs {
			if slices.Contains(names, d) {
				return true
			}
		}
		return false
	}
	ext := ""
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		ext = base[i:]
	}
	switch {
	case ext == ".md" || ext == ".mdx" || ext == ".mdc" || ext == ".rst" || ext == ".adoc",
		inDir("docs"), strings.HasPrefix(base, "readme"):
		return index("docs")
	case strings.Contains(base, ".test.") || strings.Contains(base, ".spec."),
		inDir("__tests__", "test", "tests", "testdata"),
		strings.HasSuffix(base, "_test.go"), strings.HasSuffix(base, "_test.py"), strings.HasPrefix(base, "test_") && ext == ".py",
		strings.HasSuffix(base, "_spec.rb"):
		return index("tests")
	case len(dirs) == 0 && (strings.Contains(base, ".config.") || strings.HasPrefix(base, ".git") ||
		strings.HasPrefix(base, ".") && (strings.HasSuffix(base, "rc") || strings.Contains(base, "rc."))),
		len(dirs) > 0 && dirs[0] == ".github",
		strings.HasPrefix(base, "tsconfig") && ext == ".json",
		base == "dockerfile", base == ".dockerignore", base == "makefile",
		strings.HasPrefix(base, "docker-compose") && (ext == ".yml" || ext == ".yaml"):
		return index("config")
	case slices.Contains([]string{"package.json", "pnpm-workspace.yaml", "cargo.toml", "go.mod", "gemfile", "requirements.txt"}, base):
		return index("deps")
	case f.Generated, inDir("dist"), ext == ".lock", strings.Contains(base, ".generated."):
		return index("generated")
	}
	return index("code")
}

// grouping returns how the files group now: as the user chose, else as
// fits the source.
func (w *window) grouping() groupMode {
	an := w.analysisResult()
	mode := w.groupPref
	if mode == groupAI && an == nil {
		mode = groupAuto
	}
	if mode != groupAuto {
		return mode
	}
	switch {
	case an != nil:
		return groupAI
	case w.source.kind == sourcePull:
		return groupKind
	}
	return groupNone
}

// setGrouping groups the files another way.
func (w *window) setGrouping(mode groupMode) {
	w.groupPref = mode
	w.regroupSoon()
}

// regroupSoon groups the files again as the next frame starts: the list
// may be building rows of the files in their order now.
func (w *window) regroupSoon() {
	w.regroupPending = true
	w.invalidate()
}

// regroup groups the files, and orders them by their groups: the tree,
// the surface and the hunks follow the order.
func (w *window) regroup() {
	path := func(i int) string {
		if i >= 0 && i < len(w.files) {
			return w.files[i].Path
		}
		return ""
	}
	current, selected := path(w.current), path(w.selFile)
	w.groups = nil
	for _, f := range w.files {
		f.group = -1
	}
	switch w.grouping() {
	case groupKind:
		used := make([]int, len(kinds))
		for i := range used {
			used[i] = -1
		}
		ranks := make([]int, len(w.files))
		for i, f := range w.files {
			ranks[i] = kindOf(f.File)
		}
		// The groups in the kinds' order, the empty ones left out.
		for k := range kinds {
			if slices.Contains(ranks, k) {
				used[k] = len(w.groups)
				w.groups = append(w.groups, fileGroup{key: kinds[k].key, label: kinds[k].label, summary: kinds[k].summary})
			}
		}
		for i, f := range w.files {
			f.group = used[ranks[i]]
		}
	case groupAI:
		an := w.analysisResult()
		byPath := map[string]int{}
		for gi, g := range an.Groups {
			w.groups = append(w.groups, fileGroup{key: g.Key, label: g.Label, summary: g.Summary, critical: g.Critical, ai: true})
			for _, p := range g.FilePaths {
				if _, ok := byPath[p]; !ok {
					byPath[p] = gi
				}
			}
		}
		rest := -1
		for _, f := range w.files {
			gi, ok := byPath[f.Path]
			if !ok && f.OldPath != f.Path {
				gi, ok = byPath[f.OldPath]
			}
			if !ok {
				// Files new since the review gather at the end.
				if rest < 0 {
					rest = len(w.groups)
					w.groups = append(w.groups, fileGroup{key: "unreviewed", label: "Not in the review", summary: "Files that changed since the agent's review.", ai: true})
				}
				gi = rest
			}
			f.group = gi
		}
		// Groups left without files go, the indices following.
		count := make([]int, len(w.groups))
		for _, f := range w.files {
			count[f.group]++
		}
		remap := make([]int, len(w.groups))
		var kept []fileGroup
		for gi, g := range w.groups {
			remap[gi] = -1
			if count[gi] > 0 {
				remap[gi] = len(kept)
				kept = append(kept, g)
			}
		}
		w.groups = kept
		for _, f := range w.files {
			f.group = remap[f.group]
		}
	}
	slices.SortStableFunc(w.files, func(a, b *fileState) int {
		if a.group != b.group {
			return a.group - b.group
		}
		return git.ComparePaths(a.Path, b.Path)
	})
	w.current, w.selFile = 0, -1
	for i, f := range w.files {
		if f.Path == current {
			w.current = i
		}
		if f.Path == selected {
			w.selFile = i
		}
	}
	if w.selFile < 0 {
		w.selHunk = -1
	}
	w.buildNotes()
	w.matchesFor = "\x00" // find again
	w.buildTree()
	w.rowsDirty = true
}

// groupFiles returns the indices of a group's files.
func (w *window) groupFiles(gi int) []int {
	var out []int
	for i, f := range w.files {
		if f.group == gi {
			out = append(out, i)
		}
	}
	return out
}

// revealGroup scrolls the surface to a group's header.
func (w *window) revealGroup(gi int) {
	if w.rowsDirty {
		w.buildRows()
	}
	for r := range w.rows {
		if w.rows[r].kind == rowGroup && int(w.rows[r].gap) == gi {
			w.list.ScrollTo(r, ui.Start)
			w.current = int(w.rows[r].file)
			w.revealedAt = w.now
			return
		}
	}
}

// groupMark draws the mark of a group: a sparkle for the agent's groups.
func groupIcon(g *fileGroup) *ui.SVG {
	if g.ai {
		return iconSparkle
	}
	return iconLayers
}

// scopeBar shows the groups as chips, with how many of their files were
// viewed; a chip scrolls to its group.
func (w *window) scopeBar(c *ui.Context, pal *palette) {
	if len(w.groups) == 0 {
		return
	}
	t := c.Theme()
	active := -1
	if w.current >= 0 && w.current < len(w.files) {
		active = w.files[w.current].group
	}
	viewed := make([]int, len(w.groups))
	total := make([]int, len(w.groups))
	for _, f := range w.files {
		if f.group < 0 {
			continue
		}
		total[f.group]++
		if w.isViewed(f) {
			viewed[f.group]++
		}
	}
	ui.ScrollHorizontal(c).Shrink(0).Padding(8, 12, 0).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			for gi := range w.groups {
				g := &w.groups[gi]
				tip := g.label
				if g.summary != "" {
					tip += ": " + g.summary
				}
				b := ui.ButtonBase(c).Key(g.key).Height(26).Padding(0, 10, 0, 8).Gap(6).Radius(13).Label(g.label).Tooltip(tip).
					Border(1, ui.RGBA(127, 127, 127, 0.18)).Shrink(0)
				switch {
				case gi == active:
					b.Background(t.Accent.Alpha(0.14)).Border(1, t.Accent.Alpha(0.45))
				case b.Hovered():
					b.Background(pal.hover)
				}
				if b.Clicked() {
					w.revealGroup(gi)
				}
				done := viewed[gi] == total[gi] && total[gi] > 0
				b.Children(func() {
					icon := ui.Icon(c, groupIcon(g)).FontSize(12).TextColor(t.TextMuted)
					if g.critical {
						icon.TextColor(t.Danger)
					}
					ui.Text(c, g.label).FontSize(12).FontWeight(600).SingleLine()
					count := ui.Textf(c, "%d/%d", viewed[gi], total[gi]).Font(w.codeFont()).FontSize(10).FontWeight(600).TextColor(t.TextMuted)
					if done {
						count.TextColor(pal.viewed)
					}
				})
			}
		})
	})
}

// groupRow is the header of a group on the surface: its name, why it is,
// and how much of it was viewed.
func (w *window) groupRow(c *ui.Context, pal *palette, gi int) {
	t := c.Theme()
	if gi < 0 || gi >= len(w.groups) {
		ui.Box(c).Height(0)
		return
	}
	g := &w.groups[gi]
	files := w.groupFiles(gi)
	viewed, adds, dels := 0, 0, 0
	allCollapsed := true
	for _, i := range files {
		f := w.files[i]
		if w.isViewed(f) {
			viewed++
		}
		if !f.collapsed {
			allCollapsed = false
		}
		if countable(f) {
			adds += f.Additions
			dels += f.Deletions
		}
	}
	ui.Column(c).Padding(20, 2, 0).Gap(4).Background(pal.appBg).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			icon := ui.Icon(c, groupIcon(g)).FontSize(15).TextColor(t.TextMuted)
			if g.critical {
				icon.TextColor(t.Danger)
			}
			ui.Text(c, g.label).FontSize(15).Bold().SingleLine().Shrink(1).MinWidth(0)
			if g.critical {
				ui.Text(c, "Needs care").FontSize(11).FontWeight(600).Padding(2, 8).Radius(10).
					Background(t.Danger.Alpha(0.14)).TextColor(t.Danger).Shrink(0)
			}
			ui.RichText(c,
				ui.Span{Text: plural(len(files), "file") + " · ", Color: t.TextMuted},
				ui.Span{Text: "+" + thousands(adds), Color: pal.addText},
				ui.Span{Text: " −" + thousands(dels), Color: pal.delText},
			).FontSize(12).FontWeight(600).Shrink(0)
			ui.Spacer(c)
			count := ui.Textf(c, "%d/%d viewed", viewed, len(files)).FontSize(12).TextColor(t.TextMuted).Shrink(0)
			if viewed == len(files) && viewed > 0 {
				count.TextColor(pal.viewed)
			}
			label := "Collapse"
			if allCollapsed {
				label = "Expand"
			}
			b := ui.ButtonBase(c).Padding(3, 8).Radius(6).Label(label + " " + g.label).Children(func() {
				ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(t.TextMuted)
			})
			if b.Hovered() {
				b.Background(pal.hover)
			}
			if b.Clicked() {
				for _, i := range files {
					w.files[i].collapsed = !allCollapsed
				}
				w.rowsDirty = true
			}
		})
		if g.summary != "" {
			ui.Text(c, g.summary).FontSize(13).TextColor(t.Text.Alpha(0.8)).Selectable().Padding(0, 0, 0, 23)
		}
	})
}

// aiNote is a note of the agent's review on a file, or on a line of it.
type aiNote struct {
	path     string
	file     bool // on the whole file
	side     side
	line     int
	text     string
	critical bool
}

// buildNotes gathers the notes of the agent's review on files unchanged
// since: on others, the lines they are on may have moved.
func (w *window) buildNotes() {
	w.notes = nil
	st := w.analyses[w.source]
	if st == nil || st.result == nil {
		return
	}
	current := map[string]bool{}
	for _, f := range w.files {
		if fp, ok := st.fingerprints[f.Path]; !ok || fp == f.Fingerprint {
			current[f.Path] = true
		}
	}
	w.notes = map[string][]*aiNote{}
	for _, g := range st.result.Groups {
		for _, n := range g.FileNotes {
			if current[n.Path] {
				w.notes[n.Path] = append(w.notes[n.Path], &aiNote{path: n.Path, file: true, text: n.Text, critical: n.Critical})
			}
		}
		for _, n := range g.LineNotes {
			if !current[n.Path] {
				continue
			}
			sd := sideNew
			if n.Side == "deletions" {
				sd = sideOld
			}
			w.notes[n.Path] = append(w.notes[n.Path], &aiNote{path: n.Path, side: sd, line: n.Line, text: n.Text, critical: n.Critical})
		}
	}
}

// noteRow shows a note of the agent's review, under the line it is on or
// atop the file.
func (w *window) noteRow(c *ui.Context, pal *palette, n *aiNote) {
	t := c.Theme()
	accent := t.Accent
	if n.critical {
		accent = t.Danger
	}
	w.card(c, pal).Padding(6, 16).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Gap(10).Padding(8, 12).Radius(10).AlignItems(ui.Start).
			Background(accent.Alpha(0.07)).Border(1, accent.Alpha(0.22)).Children(func() {
			ui.Icon(c, iconSparkle).FontSize(14).TextColor(accent).Margin(1, 0, 0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				label := "AI note"
				switch {
				case n.file:
					label = "AI note on this file"
				case n.side == sideOld:
					label = "AI note · old line " + itoa(n.line)
				default:
					label = "AI note · line " + itoa(n.line)
				}
				if n.critical {
					label += " · needs care"
				}
				ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(accent)
				ui.Text(c, n.text).FontSize(13).Selectable()
			})
		})
	})
}

// analysisFiles describes the files to an agent.
func analysisFiles(files []*fileState) []agent.File {
	out := make([]agent.File, 0, len(files))
	for _, f := range files {
		if f.Directory {
			continue
		}
		status := byte(f.Status)
		if f.Status == diff.Untracked {
			status = 'A'
		}
		out = append(out, agent.File{Path: f.Path, OldPath: f.OldPath, Status: status, Additions: f.Additions, Deletions: f.Deletions,
			Binary: f.Binary, Generated: f.Generated, Patch: patchText(f.File)})
	}
	return out
}

// patchText writes a file's hunks as a unified diff.
func patchText(f *diff.File) string {
	if f.TooLarge || f.Binary {
		return ""
	}
	var b strings.Builder
	for _, h := range f.Hunks {
		b.WriteString("@@ -" + itoa(h.OldStart) + "," + itoa(h.OldLines) + " +" + itoa(h.NewStart) + "," + itoa(h.NewLines) + " @@")
		if h.Section != "" {
			b.WriteString(" " + h.Section)
		}
		b.WriteByte('\n')
		for _, l := range h.Lines {
			switch l.Kind {
			case diff.Add:
				b.WriteByte('+')
			case diff.Del:
				b.WriteByte('-')
			default:
				b.WriteByte(' ')
			}
			b.WriteString(strings.TrimRight(l.Text, "\r\n"))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func itoa(n int) string { return strconv.Itoa(n) }
