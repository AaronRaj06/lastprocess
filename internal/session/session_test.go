package session

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/AaronRaj06/lastprocess/internal/match"
	"github.com/AaronRaj06/lastprocess/internal/render"
	"github.com/AaronRaj06/lastprocess/internal/store"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

// startServer runs a real SSH server on a free port with fast ticks.
func startServer(t *testing.T) (addr string, srv *Server) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	srv = &Server{Store: st, MaxSessions: 4, MaxPerIP: 4, AllowReset: true, ReplayTick: time.Millisecond}
	srv.Mgr = match.NewManager(match.Config{
		Size: 4, Fill: []string{"baseline"}, MaxMatches: 4, LobbyTicks: 10, ResultTicks: 10, Tick: 2 * time.Millisecond,
		OnEnd: func(r match.Result) int64 {
			if !slices.ContainsFunc(r.Slots, func(s match.SlotInfo) bool { return s.Owner != nil }) {
				return 0
			}
			winKey := ""
			if r.Winner >= 0 && r.Slots[r.Winner].Owner != nil {
				winKey = r.Slots[r.Winner].Owner.Key
			}
			id, _ := st.SaveMatch(r.Ticks, "w", winKey, nil, r.Log.Encode())
			return id
		},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSSH(srv, ln.Addr().String(), filepath.Join(t.TempDir(), "host_ed25519"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(func() { _ = s.Close(); _ = st.Close() })
	return ln.Addr().String(), srv
}

func dial(t *testing.T, addr, user string, withKey bool) *gossh.Client {
	t.Helper()
	cfg := &gossh.ClientConfig{User: user, HostKeyCallback: gossh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second}
	if withKey {
		_, priv, _ := ed25519.GenerateKey(rand.Reader)
		signer, _ := gossh.NewSignerFromKey(priv)
		cfg.Auth = []gossh.AuthMethod{gossh.PublicKeys(signer)}
	} else {
		cfg.Auth = []gossh.AuthMethod{gossh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) { return nil, nil })}
	}
	c, err := gossh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func run(t *testing.T, c *gossh.Client, cmd, stdin string) (string, error) {
	t.Helper()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	s.Stdin = strings.NewReader(stdin)
	out, err := s.CombinedOutput(cmd)
	return string(out), err
}

// terminal is an interactive session whose output feeds a screen emulator.
type terminal struct {
	in  interface{ Write([]byte) (int, error) }
	scr *render.Screen
	ch  chan []byte
}

func openTerminal(t *testing.T, c *gossh.Client, cmd string) *terminal {
	t.Helper()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.RequestPty("xterm", 43, 80, gossh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	in, _ := s.StdinPipe()
	out, _ := s.StdoutPipe()
	if cmd == "" {
		err = s.Shell()
	} else {
		err = s.Start(cmd)
	}
	if err != nil {
		t.Fatal(err)
	}
	term := &terminal{in: in, scr: render.NewScreen(80, 43), ch: make(chan []byte, 256)}
	go func() {
		defer close(term.ch)
		for {
			buf := make([]byte, 8192)
			n, err := out.Read(buf)
			if n > 0 {
				term.ch <- buf[:n]
			}
			if err != nil {
				return
			}
		}
	}()
	return term
}

// until pumps output into the screen until the screen contains want.
func (tm *terminal) until(t *testing.T, want string) string {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		all := string(bytes.Join(tm.scr.Rows, []byte("\n")))
		if strings.Contains(all, want) {
			return all
		}
		select {
		case b, ok := <-tm.ch:
			if !ok {
				t.Fatalf("session closed before %q appeared; screen:\n%s", want, all)
			}
			_, _ = tm.scr.Write(b)
		case <-deadline:
			t.Fatalf("timed out waiting for %q; screen:\n%s", want, all)
		}
	}
}

// Two real SSH clients (one with a key, one guest) play in the same match, one
// brings an uploaded Lua bot, and the finished match can be replayed.
func TestTwoClientsPlayOneMatch(t *testing.T) {
	addr, srv := startServer(t)
	ann, bob := dial(t, addr, "Ann!", true), dial(t, addr, "bob", false)

	if out, err := run(t, ann, "upload-bot turret", `function act() return "shoot_e" end`); err != nil || !strings.Contains(out, "stored turret") {
		t.Fatalf("upload: %q %v", out, err)
	}
	if out, _ := run(t, ann, "bots", ""); strings.TrimSpace(out) != "turret" {
		t.Fatalf("bots: %q", out)
	}
	for cmd, stdin := range map[string]string{
		"upload-bot bad":    `while true do end`,
		"upload-bot BAD/..": `function act() end`,
		"upload-bot big":    `function act() end --` + strings.Repeat("x", 20000),
		"play nosuchbot":    "",
		"frobnicate":        "",
	} {
		if out, err := run(t, ann, cmd, stdin); err == nil || !strings.Contains(out, "error:") {
			t.Errorf("%s: accepted: %q", cmd, out)
		}
	}
	if out, err := run(t, bob, "upload-bot x", `function act() end`); err == nil {
		t.Fatalf("guest upload accepted: %q", out)
	}
	if out, err := run(t, bob, "play", ""); err == nil || !strings.Contains(out, "ssh -t") {
		t.Fatalf("play without a terminal: %q", out)
	}

	ta := openTerminal(t, ann, "play turret")
	tb := openTerminal(t, bob, "")
	lobby := ta.until(t, "in the lobby:")
	screenA := ta.until(t, "you are @ ann")
	tb.until(t, "you are @ bob")
	if !strings.Contains(lobby, "ann") || !strings.Contains(screenA, "ann/turret") || !strings.Contains(screenA, "B:") {
		t.Fatalf("screen:\n%s", screenA)
	}
	// Input reaches the sim: the ack counter in the HUD goes up.
	for i := 0; i < 200 && !strings.Contains(string(bytes.Join(ta.scr.Rows, nil)), "ack 3"); i++ {
		_, _ = ta.in.Write([]byte("d"))
		_, _ = tb.in.Write([]byte("\x1b[A"))
		time.Sleep(3 * time.Millisecond)
		select {
		case b := <-ta.ch:
			_, _ = ta.scr.Write(b)
		default:
		}
	}
	ta.until(t, "ack 3")

	var stats Stats
	out, _ := run(t, bob, "stats reset", "")
	if err := json.Unmarshal([]byte(out), &stats); err != nil || stats.Sessions != 3 || stats.Frames == 0 || stats.Ticks == 0 {
		t.Fatalf("stats: %q %+v", out, stats)
	}
	if got := srv.Stats(false); got.Frames > stats.Frames {
		t.Fatalf("reset did not clear the counters: %+v", got)
	}

	done := ta.until(t, "match #1")
	if !strings.Contains(done, "WIN") && !strings.Contains(done, "nobody survived") {
		t.Fatalf("results screen:\n%s", done)
	}
	_, _ = ta.in.Write([]byte("q"))
	_, _ = tb.in.Write([]byte{3})

	if out, _ := run(t, ann, "replay", ""); !strings.Contains(out, "#1") {
		t.Fatalf("replay list: %q", out)
	}
	tr := openTerminal(t, ann, "replay 1")
	tr.until(t, "replay of match #1 (verified")
	tr.until(t, "next in")
	if out, err := run(t, ann, "leaderboard", ""); err != nil {
		t.Fatalf("leaderboard: %q %v", out, err)
	}
	if out, _ := run(t, ann, "help", ""); !strings.Contains(out, "upload-bot") {
		t.Fatalf("help: %q", out)
	}
}

func TestSessionCap(t *testing.T) {
	addr, _ := startServer(t)
	c := dial(t, addr, "x", false)
	for range 4 {
		openTerminal(t, c, "spectate").until(t, "alive")
	}
	if out, err := run(t, c, "help", ""); err == nil || !strings.Contains(out, "full") {
		t.Fatalf("fifth session admitted: %q", out)
	}
}

func TestKeyParser(t *testing.T) {
	var k keyParser
	var got []world.Action
	var evs []event
	for _, b := range []byte("wD\x1b[A\x1bOCzq\x1bn") {
		a, ev := k.feed(b)
		if a != world.Stay {
			got = append(got, a)
		}
		if ev != evNone {
			evs = append(evs, ev)
		}
	}
	want := []world.Action{world.MoveN, world.DashE, world.ShootN, world.ShootE}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("actions %v", got)
	}
	if len(evs) != 2 || evs[0] != evQuit || evs[1] != evRejoin {
		t.Fatalf("events %v", evs)
	}
	if cleanName("Root; DROP") != "rootdrop" || cleanName("!!!") != "guest" || len(cleanName(strings.Repeat("a", 99))) != 12 {
		t.Fatal("cleanName")
	}
}
