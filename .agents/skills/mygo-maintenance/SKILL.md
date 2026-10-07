---
name: mygo-maintenance
description: Maintain Go applications using github.com/egoist/mygo, including UI changes, crash fixes, and dependency upgrades. Apply MyGo-specific lifetime, identity, threading, and testing practices.
---

# MyGo app maintenance

Check the app's pinned MyGo version and existing conventions before changing APIs. Locate its source with `go list -m -f '{{.Dir}}' github.com/egoist/mygo`; read `ui/doc.go` and relevant implementation comments rather than assuming the latest API.

## UI lifetime and identity

- `ui.Context` and `ui.Element` are temporary build/frame objects. Keep them out of background work and cross-frame state. Reset any fields sharing current-frame elements at the start of every build, including rebuild passes.
- Build an element before querying its shortcuts or focus. Hidden, empty, loading, error, and alternate views must leave absent element references nil. Store semantic keys and pending focus requests between frames.
- `ui.List` builds rows before returning its element. For focus styling inside row builders, use a current-frame parent scope rather than a previous list pointer.
- Preserve identity when siblings change: give containers stable, unique `.Key(...)` values immediately after creation, before building children. Widgets that process input during construction cannot be keyed directly; key their surrounding container. Use stable item keys in `ListState.Key`.
- A non-nil pointer can refer to cleared or reused storage. Check lifetime ownership before adding nil guards or panic recovery; nil-analysis tools do not establish frame validity.

## State, work, and rendering

- Views may rebuild several times for one event. Trigger external side effects through handled actions or explicit guarded jobs, not unconditional rendering code.
- Keep slow I/O and expensive computation off the UI thread. Publish results through `Window.Update`; it is asynchronous. `Invalidate` requests redraws without synchronizing model writes. Cancel obsolete jobs or reject stale results after source changes or window closure.
- Keep drawing callbacks limited to painting; they can run repeatedly. Prefer MyGo widgets/base widgets to preserve keyboard, focus, disabled, and accessibility behavior. Follow the app's theme and sizing conventions.

## Verification

- Reproduce UI failures with `ui.NewTester`. Exercise extra frames while elements disappear and return, plus relevant focus, typing, and shortcut behavior.
- Run targeted regression tests, then `go test ./...`. Use race checks when changing shared state, and inspect rendered output or the native app when changing layout/platform behavior.
- For upgrades, verify affected API/lifecycle assumptions against the new pinned source and rerun relevant UI tests. Report remaining platform-specific verification limits.
