package main

import (
	"log"
	"os"
	"time"

	"github.com/egoist/mygo"
)

// captureIfAsked writes a PNG of the window to $GODIFF_CAPTURE once it has
// loaded, then quits: for checking how the app looks from scripts.
func (w *window) captureIfAsked() {
	path := os.Getenv("GODIFF_CAPTURE")
	if path == "" {
		return
	}
	go func() {
		time.Sleep(2500 * time.Millisecond)
		if setup := os.Getenv("GODIFF_CAPTURE_SETUP"); setup != "" {
			w.win.Update(func() { w.debugSetup(setup) })
			time.Sleep(1200 * time.Millisecond)
		}
		png, err := w.win.CapturePage()
		if err != nil {
			log.Println("capture:", err)
		} else if err := os.WriteFile(path, png, 0o644); err != nil {
			log.Println("capture:", err)
		}
		mygo.App.Quit()
	}()
}

// debugSetup puts the window in a state to capture.
func (w *window) debugSetup(setup string) {
	switch setup {
	case "unified":
		w.settings.DiffStyle = "unified"
		w.rowsDirty = true
	case "history":
		w.tab = 1
	case "commit":
		w.toggleCommit()
	case "palette":
		w.paletteOpen = true
	case "find":
		w.finding = true
		w.query = "grip"
	case "comment":
		w.nextHunk(1)
		w.commentOnSelection()
		for _, c := range w.comments {
			c.text = "Should the grip be wider on vertical splits too?"
		}
	case "wrap":
		w.settings.WordWrap = true
		w.rowsDirty = true
	}
}
