package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// Settings are the user's preferences, in ~/.godiff/godiff.jsonc as
// codiff keeps its own in ~/.codiff/codiff.jsonc.
type Settings struct {
	CodeFontFamily       string `json:"codeFontFamily"`
	CodeFontSize         int    `json:"codeFontSize"`
	CopyCommentsOnClose  bool   `json:"copyCommentsOnClose"`
	DiffStyle            string `json:"diffStyle"` // split or unified
	EditorCommand        string `json:"editorCommand"`
	ReviewCommentsPrefix string `json:"reviewCommentsPrefix"`
	SidebarPosition      string `json:"sidebarPosition"` // left or right
	ShowWhitespace       bool   `json:"showWhitespace"`
	Theme                string `json:"theme"` // system, light or dark
	WordWrap             bool   `json:"wordWrap"`
}

func defaultSettings() Settings {
	return Settings{
		CodeFontSize:         13,
		DiffStyle:            "split",
		ReviewCommentsPrefix: "# Address these Review Comments",
		SidebarPosition:      "left",
		Theme:                "system",
	}
}

// normalize keeps the settings within what they may be.
func (s *Settings) normalize() {
	d := defaultSettings()
	s.CodeFontFamily = strings.TrimSpace(s.CodeFontFamily)
	if s.CodeFontSize == 0 {
		s.CodeFontSize = d.CodeFontSize
	}
	s.CodeFontSize = min(max(s.CodeFontSize, 10), 32)
	if s.DiffStyle != "split" && s.DiffStyle != "unified" {
		s.DiffStyle = d.DiffStyle
	}
	if s.SidebarPosition != "left" && s.SidebarPosition != "right" {
		s.SidebarPosition = d.SidebarPosition
	}
	if s.Theme != "system" && s.Theme != "light" && s.Theme != "dark" {
		s.Theme = d.Theme
	}
	if s.ReviewCommentsPrefix == "" {
		s.ReviewCommentsPrefix = d.ReviewCommentsPrefix
	}
}

// config holds the settings, shared by the windows, and keeps them in
// step with the file.
type config struct {
	mu       sync.Mutex
	path     string
	settings Settings
	modTime  time.Time
	onChange map[int]func(Settings)
	nextID   int
}

var cfg = newConfig()

func newConfig() *config {
	home, _ := os.UserHomeDir()
	c := &config{path: filepath.Join(home, ".godiff", "godiff.jsonc"), settings: defaultSettings()}
	c.load()
	return c
}

func (c *config) Get() Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings
}

// Update changes the settings and writes them.
func (c *config) Update(fn func(s *Settings)) {
	c.mu.Lock()
	fn(&c.settings)
	c.settings.normalize()
	s := c.settings
	c.mu.Unlock()
	c.write(s)
	c.notify(s)
}

// OnChange calls fn whenever the settings change, from any goroutine, and
// returns a function that stops it.
func (c *config) OnChange(fn func(Settings)) (off func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onChange == nil {
		c.onChange = map[int]func(Settings){}
	}
	c.nextID++
	id := c.nextID
	c.onChange[id] = fn
	return func() {
		c.mu.Lock()
		delete(c.onChange, id)
		c.mu.Unlock()
	}
}

func (c *config) notify(s Settings) {
	c.mu.Lock()
	fns := make([]func(Settings), 0, len(c.onChange))
	for _, fn := range c.onChange {
		fns = append(fns, fn)
	}
	c.mu.Unlock()
	for _, fn := range fns {
		fn(s)
	}
}

func (c *config) load() bool {
	fi, err := os.Stat(c.path)
	if err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if fi.ModTime().Equal(c.modTime) {
		return false
	}
	c.modTime = fi.ModTime()
	data, err := os.ReadFile(c.path)
	if err != nil {
		return false
	}
	var file struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(stripJSONC(data), &file); err != nil {
		log.Printf("godiff: %s: %v", c.path, err)
		return false
	}
	s := defaultSettings()
	if len(file.Settings) > 0 {
		// Fields of the wrong type keep their defaults.
		var fields map[string]json.RawMessage
		json.Unmarshal(file.Settings, &fields)
		for k, v := range fields {
			one, _ := json.Marshal(map[string]json.RawMessage{k: v})
			json.Unmarshal(one, &s)
		}
	}
	s.normalize()
	c.settings = s
	return true
}

func (c *config) write(s Settings) {
	data, err := json.MarshalIndent(map[string]any{"settings": s}, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(c.path, append(data, '\n'), 0o644); err != nil {
		log.Printf("godiff: %v", err)
		return
	}
	if fi, err := os.Stat(c.path); err == nil {
		c.mu.Lock()
		c.modTime = fi.ModTime()
		c.mu.Unlock()
	}
}

// ensure writes the file with the defaults when there is none, for the
// user to edit.
func (c *config) ensure() string {
	if _, err := os.Stat(c.path); err != nil {
		c.write(c.Get())
	}
	return c.path
}

// watch reads the file again when it changes.
func (c *config) watch() {
	go func() {
		for range time.Tick(time.Second) {
			if c.load() {
				c.notify(c.Get())
			}
		}
	}()
}

// stripJSONC removes the comments and trailing commas of JSONC.
func stripJSONC(data []byte) []byte {
	var out bytes.Buffer
	inString := false
	for i := 0; i < len(data); i++ {
		ch := data[i]
		if inString {
			out.WriteByte(ch)
			if ch == '\\' && i+1 < len(data) {
				i++
				out.WriteByte(data[i])
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch {
		case ch == '"':
			inString = true
			out.WriteByte(ch)
		case ch == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
		case ch == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i++
		case ch == ',':
			// A comma before a closing bracket goes.
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
			out.WriteByte(ch)
		default:
			out.WriteByte(ch)
		}
	}
	return out.Bytes()
}

// store keeps what the app remembers apart from the settings: the files
// viewed in each repository, and the layout of the window.
type store struct {
	mu   sync.Mutex
	path string
	data storeData
}

type storeData struct {
	// Viewed maps repository roots to the fingerprints of the files
	// viewed, by path.
	Viewed       map[string]map[string]string `json:"viewed"`
	SidebarWidth float32                      `json:"sidebarWidth"`
	SidebarShown *bool                        `json:"sidebarShown"`
	// LastRepository is the repository opened last, which the app opens
	// when started without one, from the Dock.
	LastRepository string `json:"lastRepository"`
}

var state = &store{}

func (s *store) open() {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return
	}
	s.path = filepath.Join(dir, "state.json")
	if data, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(data, &s.data)
	}
	if s.data.Viewed == nil {
		s.data.Viewed = map[string]map[string]string{}
	}
}

func (s *store) viewed(root string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.data.Viewed[root] {
		out[k] = v
	}
	return out
}

func (s *store) setViewed(root, path, fingerprint string) {
	s.mu.Lock()
	if s.data.Viewed == nil {
		s.data.Viewed = map[string]map[string]string{}
	}
	m := s.data.Viewed[root]
	if m == nil {
		m = map[string]string{}
		s.data.Viewed[root] = m
	}
	if fingerprint == "" {
		delete(m, path)
	} else {
		m[path] = fingerprint
	}
	s.mu.Unlock()
	s.save()
}

func (s *store) layout() (width float32, shown bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	width, shown = s.data.SidebarWidth, true
	if width == 0 {
		width = 280
	}
	if s.data.SidebarShown != nil {
		shown = *s.data.SidebarShown
	}
	return
}

func (s *store) setLayout(width float32, shown bool) {
	s.mu.Lock()
	s.data.SidebarWidth, s.data.SidebarShown = width, &shown
	s.mu.Unlock()
	s.save()
}

func (s *store) lastRepository() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.LastRepository
}

func (s *store) setLastRepository(root string) {
	s.mu.Lock()
	s.data.LastRepository = root
	s.mu.Unlock()
	s.save()
}

func (s *store) save() {
	if s.path == "" {
		return
	}
	s.mu.Lock()
	data, err := json.Marshal(s.data)
	s.mu.Unlock()
	if err == nil {
		os.WriteFile(s.path, data, 0o644)
	}
}
