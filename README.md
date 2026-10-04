# Godiff

A native, minimal, local diff viewer for reviewing Git changes and committing
them: a reimplementation of [codiff](https://github.com/nkzw-tech/codiff) in Go
with [MyGo](https://mygo.egoist.dev/)'s native UI. No webview, no JavaScript:
the window is drawn by MyGo on the GPU, with the system's fonts, accent color,
dark mode, menus and vibrancy.

## Features

- **Review local changes**: staged, unstaged and untracked files against
  `HEAD`, in one scrolling surface of file cards with sticky headers.
- **Split and unified diffs** with syntax highlighting (codiff's Licht and
  Dunkel palettes), word-level change highlighting, and unchanged lines that
  expand 100 at a time or all at once. New and deleted files show in one
  column.
- **Viewed files**: marking a file viewed collapses it, and it opens again
  once it changes. Generated files (lockfiles, `*.min.js`,
  `linguist-generated`, …) and dependency folders start collapsed.
- **Review comments**: click a line, or press <kbd>J</kbd>/<kbd>K</kbd> to
  choose a hunk and <kbd>Enter</kbd>, then copy every comment as Markdown,
  with the diff around it, for an agent or a colleague.
- **Commit** the files you choose, with a subject and a summary, from the
  sidebar's Commit button. Other staged work stays staged.
- **History**: browse commits and review any of them, or compare the work
  tree with a branch.
- **Find in diffs** (<kbd>⌘F</kbd>), a **file filter** (<kbd>⌘P</kbd>) and a
  **command bar** (<kbd>⌘K</kbd>).
- Image previews of changed pictures, a file tree with change counts and
  status letters, and a banner when the work tree changes.

## Usage

```sh
go run . [<commit> | <branch>] [<path>]
```

- `godiff` reviews the uncommitted changes of the repository you are in.
- `godiff HEAD~1` reviews a commit, against its first parent.
- `godiff main` compares the work tree, committed or not, with where it
  branched off `main`.
- `godiff ../other-repo` opens another repository.

Every repository opens in a window of its own. **Godiff → Install Command Line
Tool…** installs a `godiff` command that opens the app on the repository it
runs in.

### Keyboard

| Keys | |
|---|---|
| <kbd>⌘K</kbd> | Command bar |
| <kbd>⌘P</kbd> | Filter files |
| <kbd>⌘F</kbd> | Find in diffs |
| <kbd>J</kbd> / <kbd>K</kbd> | Next / previous hunk |
| <kbd>Enter</kbd> | Comment on the hunk chosen |
| <kbd>⌘↩</kbd> | Add the comment; commit, in the commit view |
| <kbd>⌘⇧B</kbd> | Toggle the sidebar |
| <kbd>⌘1</kbd> / <kbd>⌘2</kbd> | Files / History |
| <kbd>⌥Z</kbd> | Toggle word wrap |
| <kbd>⌘⇧O</kbd> | Open the file in your editor |
| <kbd>⌘R</kbd> | Refresh the changes |
| <kbd>⌘+</kbd> / <kbd>⌘-</kbd> / <kbd>⌘0</kbd> | Code font size |
| <kbd>⇧?</kbd> | All the shortcuts |

## Configuration

Settings live in `~/.godiff/godiff.jsonc` (**Godiff → Open Config File…**),
with codiff's names, and apply to open windows as the file changes:

```jsonc
{
  "settings": {
    "codeFontFamily": "",          // e.g. "JetBrains Mono"; empty is SF Mono
    "codeFontSize": 13,
    "copyCommentsOnClose": false,
    "diffStyle": "split",          // or "unified"
    "editorCommand": "",           // e.g. "zed {file}:{line}"
    "reviewCommentsPrefix": "# Address these Review Comments",
    "sidebarPosition": "left",     // or "right"
    "showWhitespace": false,
    "theme": "system",             // or "light", "dark"
    "wordWrap": false
  }
}
```

Files open in `$GODIFF_EDITOR` or `editorCommand` (`{file}`, `{line}` and
`{repo}` are replaced), else VS Code, else the app the system opens them with.

## Development

```sh
go tool mygo dev          # the app, rebuilt as you edit
go test ./...             # including the views, run without a window
go tool mygo build        # Godiff.app and a disk image in build/
go run ./tools/genicon    # render resources/icon.svg to the app icon
```

`GODIFF_SNAPSHOTS=<dir> go test .` saves PNGs of the views the tests drive, and
`GODIFF_CAPTURE=<file.png>` makes the app save a picture of its window once
loaded, then quit.

The code is in a few parts:

- `internal/git`: the repository through the `git` command: changes,
  history, commits, generated files, file contents (`git cat-file --batch`).
- `internal/diff`: parsing patches, and word-level differences.
- `internal/highlight`: syntax highlighting with Chroma.
- The `main` package: the window and its views (`view.go`, `sidebar.go`,
  `diffview.go`, `commit.go`, `palette.go`), the rows of the diff surface
  (`rows.go`), comments, find, settings and menus.

## Not (yet) carried over from codiff

LLM walkthroughs, GitHub and GitLab pull request review, Markdown previews,
editing files in place, definition lookup, `a..b` ranges, and separate cards
for the staged and unstaged parts of a file (Godiff shows a file's changes
against `HEAD` as one).
