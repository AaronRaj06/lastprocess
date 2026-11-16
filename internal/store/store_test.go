package store

import (
	"fmt"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Touch("k1", "ann"))
	must(s.Touch("k2", "bob"))
	must(s.Touch("k1", "anna")) // rename
	must(s.SaveBot("k1", "b", "v1"))
	must(s.SaveBot("k1", "b", "v2")) // replace
	if got, _ := s.Bot("k1", "b"); got != "v2" {
		t.Fatalf("bot = %q", got)
	}
	if _, err := s.Bot("k2", "b"); err == nil {
		t.Fatal("bots must be per account")
	}
	for i := range MaxBotsPerAccount - 1 {
		must(s.SaveBot("k1", fmt.Sprint("x", i), "s"))
	}
	if err := s.SaveBot("k1", "one-too-many", "s"); err == nil {
		t.Fatal("bot limit not enforced")
	}
	must(s.SaveBot("k1", "b", "v3")) // replacing at the limit is still allowed
	if names, _ := s.Bots("k1"); len(names) != MaxBotsPerAccount {
		t.Fatalf("bots = %v", names)
	}

	id, err := s.SaveMatch(400, "anna", "k1", []string{"k1", "k2"}, []byte("log"))
	must(err)
	if b, _ := s.Replay(id); string(b) != "log" {
		t.Fatalf("replay = %q", b)
	}
	_, err = s.SaveMatch(100, "baseline-3", "", []string{"k2"}, []byte("x"))
	must(err)
	lb, _ := s.Leaderboard(10)
	if len(lb) != 2 || !strings.Contains(lb[0], "anna") || !strings.Contains(lb[0], "1 wins") || !strings.Contains(lb[1], "2 matches") {
		t.Fatalf("leaderboard = %q", lb)
	}
	for range KeepMatches {
		_, err = s.SaveMatch(1, "w", "", nil, []byte("x"))
		must(err)
	}
	if _, err := s.Replay(id); err == nil {
		t.Fatal("old match not pruned")
	}
	if r, _ := s.Recent(5); len(r) != 5 {
		t.Fatalf("recent = %q", r)
	}
}
