package match

import (
	"sync"
	"testing"
	"time"

	"github.com/AaronRaj06/lastprocess/internal/world"
)

func testManager(onEnd func(Result) int64) *Manager {
	return NewManager(Config{
		Size: 4, Fill: []string{"baseline", "random"}, MaxMatches: 2,
		LobbyTicks: 3, ResultTicks: 2, Tick: 200 * time.Microsecond, OnEnd: onEnd,
	})
}

func next(t *testing.T, s *Sub) *State {
	t.Helper()
	select {
	case st := <-s.C:
		return st
	case <-time.After(5 * time.Second):
		t.Fatal("no state received")
		return nil
	}
}

// Two humans and one sandboxed Lua bot share a match with server bots; the
// match runs to a result whose log replays to the same hash.
func TestLiveMatchHumansBotsAndLua(t *testing.T) {
	var mu sync.Mutex
	var results []Result
	mg := testManager(func(r Result) int64 {
		mu.Lock()
		defer mu.Unlock()
		results = append(results, r)
		return int64(len(results))
	})
	a := &Player{Name: "ann", Key: "ka", BotName: "t", BotScript: `function act() return "shoot_n" end`}
	b := &Player{Name: "bob"}
	sa, err := mg.Join(a)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := mg.Join(b)
	if err != nil {
		t.Fatal(err)
	}
	if sa.m != sb.m {
		t.Skip("lobby closed between the two joins; timing-dependent")
	}
	seen := map[Phase]bool{}
	for {
		st := next(t, sa)
		seen[st.Phase] = true
		if st.Phase == Running {
			a.Press(world.MoveE)
			b.Press(world.ShootN)
			if len(st.Slots) != 4 || st.Slots[0].Kind != "human" || st.Slots[0].Owner != a ||
				st.Slots[1].Kind != "lua" || st.Slots[1].Owner != a || st.Slots[2].Owner != b || st.Slots[3].Owner != nil {
				t.Fatalf("slots: %+v", st.Slots)
			}
		}
		if st.Phase == Results {
			if st.ID == 0 {
				t.Fatal("result not stored")
			}
			break
		}
	}
	if !seen[Lobby] || !seen[Running] || a.Acks() == 0 {
		t.Fatalf("phases %v, acks %d", seen, a.Acks())
	}
	mu.Lock()
	r := results[len(results)-1]
	mu.Unlock()
	if err := r.Log.Verify(); err != nil {
		t.Fatal(err)
	}
	if r.Ticks == 0 || len(r.Log.Names) != 4 {
		t.Fatalf("%+v", r)
	}

	// A second match is created when the first is mid-game, and the cap is enforced.
	sa.Leave()
	sb.Leave()
	var subs []*Sub
	full := false
	for i := 0; i < 50 && !full; i++ {
		s, err := mg.Join(&Player{Name: "p"})
		if err != nil {
			full = true
			break
		}
		subs = append(subs, s)
	}
	if !full {
		t.Fatal("MaxMatches not enforced")
	}
	w := mg.Watch()
	if st := next(t, w); st == nil {
		t.Fatal("spectator got nothing")
	}
	for _, s := range append(subs, w) {
		s.Leave()
	}
	// Extra matches close once empty; the permanent one stays.
	deadline := time.Now().Add(5 * time.Second)
	for mg.Stats(false).Matches != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("matches = %d", mg.Stats(false).Matches)
		}
		time.Sleep(time.Millisecond)
	}
	if s := mg.Stats(true); s.Ticks == 0 || s.Viewers != 0 {
		t.Fatalf("%+v", s)
	}
}
