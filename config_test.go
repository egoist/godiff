package main

import (
	"fmt"
	"slices"
	"testing"
)

func TestRecent(t *testing.T) {
	s := &store{}
	for i := range maxRecent + 2 {
		s.addRecent(fmt.Sprintf("/repo%d", i))
	}
	got := s.recent()
	if len(got) != maxRecent || got[0] != fmt.Sprintf("/repo%d", maxRecent+1) {
		t.Fatalf("got %v", got)
	}
	// Opening one again brings it first, once.
	if !s.addRecent("/repo5") {
		t.Error("moving a repository first changed nothing")
	}
	if s.addRecent("/repo5") {
		t.Error("the first repository changed the list")
	}
	got = s.recent()
	if got[0] != "/repo5" || len(got) != maxRecent || slices.Index(got[1:], "/repo5") >= 0 {
		t.Errorf("got %v", got)
	}
	s.clearRecent()
	if len(s.recent()) != 0 {
		t.Error("the list was not cleared")
	}
}
