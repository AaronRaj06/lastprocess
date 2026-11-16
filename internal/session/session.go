// Package session is the SSH front end: it routes a session's command
// (play, spectate, upload-bot, replay, leaderboard, ...) and, for the
// interactive ones, pumps keys in and diffed frames out.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/ssh"
	gossh "golang.org/x/crypto/ssh"

	"github.com/AaronRaj06/lastprocess/internal/ecs"
	"github.com/AaronRaj06/lastprocess/internal/match"
	"github.com/AaronRaj06/lastprocess/internal/render"
	"github.com/AaronRaj06/lastprocess/internal/replay"
	"github.com/AaronRaj06/lastprocess/internal/sandbox"
	"github.com/AaronRaj06/lastprocess/internal/store"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

const (
	enterScreen = "\x1b[?1049h\x1b[?25l\x1b[2J" // alternate screen, hide cursor
	leaveScreen = "\x1b[0m\x1b[?25h\x1b[?1049l"
	help        = `Last Process Standing - a terminal battle royale over SSH

  ssh HOST                       play (same as "play")
  ssh -t HOST play [BOT]         play; optionally bring one of your Lua bots along
  ssh -t HOST spectate [BOT]     watch; optionally enter one of your Lua bots
  ssh HOST upload-bot NAME < bot.lua
  ssh HOST bots                  list your bots
  ssh -t HOST replay [ID]        list recent matches, or replay one
  ssh HOST leaderboard
  ssh HOST stats

In a match: w a s d move, arrow keys shoot, W A S D dash, n new match, q quit.
Your account is your SSH public key; without one you play as a guest.
`
)

var nameRE = regexp.MustCompile(`^[a-z0-9_-]{1,16}$`)

// Server holds what sessions share.
type Server struct {
	Mgr         *match.Manager
	Store       *store.Store
	MaxSessions int           // total concurrent sessions
	MaxPerIP    int           // concurrent sessions per client address
	AllowReset  bool          // let "stats reset" clear the timing window (load tests only)
	ReplayTick  time.Duration // replay pacing; zero means the real 20 Hz

	sessions      atomic.Int64
	frames, bytes atomic.Uint64
	mu            sync.Mutex
	perIP         map[string]int
}

// Stats is what the stats command prints.
type Stats struct {
	match.Stats
	Sessions      int64
	Frames, Bytes uint64 // frames written to players and their total size
}

// Stats reports current load.
func (s *Server) Stats(reset bool) Stats {
	st := Stats{Stats: s.Mgr.Stats(reset), Sessions: s.sessions.Load(), Frames: s.frames.Load(), Bytes: s.bytes.Load()}
	if reset {
		s.frames.Store(0)
		s.bytes.Store(0)
	}
	return st
}

// admit enforces the global and per-address session caps.
func (s *Server) admit(sess ssh.Session) (release func(), err error) {
	ip, _, _ := net.SplitHostPort(sess.RemoteAddr().String())
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perIP == nil {
		s.perIP = map[string]int{}
	}
	if int(s.sessions.Load()) >= s.MaxSessions {
		return nil, errors.New("server is full, try again in a minute")
	}
	if s.perIP[ip] >= s.MaxPerIP {
		return nil, errors.New("too many sessions from your address")
	}
	s.perIP[ip]++
	s.sessions.Add(1)
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.sessions.Add(-1)
		if s.perIP[ip]--; s.perIP[ip] == 0 {
			delete(s.perIP, ip)
		}
	}, nil
}

// cleanName turns an SSH user name into a short display name.
func cleanName(user string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(user) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
		if b.Len() == 12 {
			break
		}
	}
	if b.Len() == 0 {
		return "guest"
	}
	return b.String()
}

// Handle serves one SSH session.
func (s *Server) Handle(sess ssh.Session) {
	release, err := s.admit(sess)
	if err == nil {
		defer release()
		err = s.route(sess)
	}
	if err != nil {
		_, _ = fmt.Fprintf(sess, "error: %v\r\n", err)
		_ = sess.Exit(1)
	}
}

func (s *Server) route(sess ssh.Session) error {
	p := &match.Player{Name: cleanName(sess.User())}
	if pk := sess.PublicKey(); pk != nil {
		p.Key = gossh.FingerprintSHA256(pk)
		if err := s.Store.Touch(p.Key, p.Name); err != nil {
			return err
		}
	}
	args := sess.Command()
	if len(args) == 0 {
		args = []string{"play"}
	}
	arg := ""
	if len(args) > 1 {
		arg = args[1]
	}
	lines := func(l []string, err error) error {
		if err == nil {
			_, err = io.WriteString(sess, strings.Join(l, "\n")+"\n")
		}
		return err
	}
	switch args[0] {
	case "play", "spectate":
		p.Spectate = args[0] == "spectate"
		if arg != "" {
			script, err := s.Store.Bot(p.Key, arg)
			if err != nil {
				return err
			}
			p.BotName, p.BotScript = arg, script
		}
		return s.play(sess, p)
	case "upload-bot":
		return s.upload(sess, p, arg)
	case "bots":
		return lines(s.Store.Bots(p.Key))
	case "replay":
		if arg == "" {
			return lines(s.Store.Recent(20))
		}
		return s.replay(sess, arg)
	case "leaderboard":
		return lines(s.Store.Leaderboard(20))
	case "stats":
		return json.NewEncoder(sess).Encode(s.Stats(arg == "reset" && s.AllowReset))
	case "help":
		_, err := io.WriteString(sess, help)
		return err
	}
	return fmt.Errorf("unknown command %q, try: help", args[0])
}

func (s *Server) upload(sess ssh.Session, p *match.Player, name string) error {
	if p.Key == "" {
		return errors.New("uploading needs an account: connect with an SSH key (ssh-keygen -t ed25519)")
	}
	if !nameRE.MatchString(name) {
		return errors.New("usage: upload-bot NAME < bot.lua   (NAME: 1-16 of a-z 0-9 _ -)")
	}
	// Read one byte past the limit so an oversized upload is detected without buffering it all.
	src, err := io.ReadAll(io.LimitReader(sess, sandbox.MaxScriptSize+1))
	if err != nil {
		return err
	}
	if err := sandbox.Check(string(src)); err != nil {
		return err
	}
	if err := s.Store.SaveBot(p.Key, name, string(src)); err != nil {
		return err
	}
	_, err = fmt.Fprintf(sess, "stored %s (%d bytes). Play with it: ssh -t HOST play %s\n", name, len(src), name)
	return err
}

func (s *Server) play(sess ssh.Session, p *match.Player) error {
	sub, err := s.Mgr.Join(p)
	if err != nil {
		return err
	}
	defer func() { sub.Leave() }()
	return s.show(sess, p, func() <-chan *match.State { return sub.C }, func() {
		if next, err := s.Mgr.Join(p); err == nil {
			sub.Leave()
			sub = next
		}
	})
}

// replay re-simulates a stored match at 20 Hz. The log is verified first, so
// what is shown is provably the match that was played.
func (s *Server) replay(sess ssh.Session, arg string) error {
	id, err := strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64)
	if err != nil {
		return errors.New("usage: replay ID")
	}
	blob, err := s.Store.Replay(id)
	if err != nil {
		return err
	}
	log, err := replay.Decode(blob)
	if err != nil {
		return err
	}
	if err := log.Verify(); err != nil {
		return err
	}
	states := make(chan *match.State, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(states)
		tick := s.ReplayTick
		if tick == 0 {
			tick = time.Second / world.TickRate
		}
		t := time.NewTicker(tick)
		defer t.Stop()
		pl := replay.NewPlayer(log)
		slots := make([]match.SlotInfo, len(log.Names))
		for i, n := range log.Names {
			slots[i].Name = n
		}
		st := &match.State{Phase: match.Running, View: pl.Sim.Snapshot(), Slots: slots, Winner: -1, ID: id}
		for left := 3 * world.TickRate; left > 0; {
			if pl.Step() {
				st = &match.State{Phase: match.Running, View: pl.Sim.Snapshot(), Slots: slots, Winner: -1, ID: id}
			} else {
				left--
				st = &match.State{Phase: match.Results, Left: left, View: st.View, Slots: slots, Winner: winner(&st.View), ID: id}
			}
			select {
			case states <- st:
			case <-done:
				return
			}
			select {
			case <-t.C:
			case <-done:
				return
			}
		}
	}()
	return s.show(sess, nil, func() <-chan *match.State { return states }, func() {})
}

func winner(v *world.View) int {
	for i, c := range v.Combatants {
		if c.ID == v.Winner && v.Winner != ecs.Nil {
			return i
		}
	}
	return -1
}

// show is the interactive loop: keys in, diffed frames out. p is nil for replays.
func (s *Server) show(sess ssh.Session, p *match.Player, states func() <-chan *match.State, rejoin func()) error {
	pty, winCh, ok := sess.Pty()
	if !ok {
		return errors.New("this command needs a terminal: use ssh -t")
	}
	w, h := pty.Window.Width, pty.Window.Height
	if w <= 0 || h <= 0 {
		w, h = 80, 24 // some clients allocate a PTY without reporting a size
	}
	keys := make(chan byte, 64)
	go func() {
		defer close(keys)
		buf := make([]byte, 64)
		for {
			n, err := sess.Read(buf)
			for _, b := range buf[:n] {
				keys <- b
			}
			if err != nil {
				return
			}
		}
	}()
	_, _ = io.WriteString(sess, enterScreen)
	defer func() { _, _ = io.WriteString(sess, leaveScreen) }()

	var prev, cur render.Frame
	var out []byte
	var kp keyParser
	for {
		select {
		case <-sess.Context().Done():
			return nil
		case win := <-winCh:
			w, h = win.Width, win.Height
			prev = render.Frame{} // size mismatch forces a full redraw
		case b, ok := <-keys:
			switch a, ev := kp.feed(b); {
			case !ok || ev == evQuit:
				return nil
			case ev == evRejoin:
				rejoin()
			case a != world.Stay && p != nil:
				p.Press(a)
			}
		case st, ok := <-states():
			if !ok {
				return nil
			}
			draw(&cur, st, p, w, h)
			out = render.Diff(out[:0], &prev, &cur)
			if _, err := sess.Write(out); err != nil {
				return nil
			}
			s.frames.Add(1)
			s.bytes.Add(uint64(len(out)))
			prev, cur = cur, prev
		}
	}
}

// draw renders one state for one viewer.
func draw(f *render.Frame, st *match.State, p *match.Player, w, h int) {
	if st.Phase == match.Lobby {
		render.Message(f, w, h,
			"L A S T   P R O C E S S   S T A N D I N G", "",
			fmt.Sprintf("match starts in %.1fs", float64(st.Left)/world.TickRate),
			"in the lobby: "+strings.Join(st.Waiting, ", "), "",
			"w a s d  move      arrow keys  shoot      W A S D  dash",
			"last one alive wins - stay inside the zone", "", "q  quit")
		return
	}
	// The viewer's own combatant: the human slot first, else their Lua bot.
	me, mine := ecs.Nil, -1
	for i, sl := range st.Slots {
		if p != nil && sl.Owner == p && (mine < 0 || sl.Kind == "human") {
			me, mine = st.View.Combatants[i].ID, i
		}
	}
	var line2 string
	switch {
	case st.Phase == match.Results:
		who := "nobody survived"
		if st.Winner >= 0 {
			who = fmt.Sprintf("WINNER: %c %s", render.Glyph(st.Winner), st.Slots[st.Winner].Name)
			if st.Winner == mine {
				who = "YOU WIN - last process standing"
			}
		}
		line2 = fmt.Sprintf("%s | match #%d | next in %.1fs", who, st.ID, float64(st.Left)/world.TickRate)
	case p == nil:
		line2 = fmt.Sprintf("replay of match #%d (verified: re-simulated to the recorded state hash) | q quit", st.ID)
	case mine >= 0 && st.View.Combatants[mine].HP > 0:
		line2 = fmt.Sprintf("wasd move  arrows shoot  WASD dash  q quit | you are @ %s | ack %d", st.Slots[mine].Name, p.Acks())
	default:
		line2 = "watching | n new match  q quit"
	}
	var roster strings.Builder
	for i, c := range st.View.Combatants {
		if c.HP > 0 {
			fmt.Fprintf(&roster, "%c:%s %d  ", render.Glyph(i), st.Slots[i].Name, c.HP)
		}
	}
	render.Draw(f, &st.View, me, w, h, render.Status(&st.View, me), line2, roster.String())
}

type event uint8

const (
	evNone event = iota
	evQuit
	evRejoin
)

// keyParser decodes the raw byte stream of a terminal: letters, and the
// three-byte escape sequences that arrow keys send.
type keyParser struct{ esc int }

var (
	moveKeys  = map[byte]world.Action{'w': world.MoveN, 'd': world.MoveE, 's': world.MoveS, 'a': world.MoveW}
	dashKeys  = map[byte]world.Action{'W': world.DashN, 'D': world.DashE, 'S': world.DashS, 'A': world.DashW}
	arrowKeys = map[byte]world.Action{'A': world.ShootN, 'C': world.ShootE, 'B': world.ShootS, 'D': world.ShootW}
)

func (k *keyParser) feed(b byte) (world.Action, event) {
	switch {
	case k.esc == 1 && (b == '[' || b == 'O'):
		k.esc = 2
		return world.Stay, evNone
	case k.esc == 2:
		k.esc = 0
		return arrowKeys[b], evNone
	case b == 0x1b:
		k.esc = 1
		return world.Stay, evNone
	}
	k.esc = 0
	switch b {
	case 'q', 3, 4: // q, ctrl-c, ctrl-d
		return world.Stay, evQuit
	case 'n':
		return world.Stay, evRejoin
	}
	if a, ok := moveKeys[b]; ok {
		return a, evNone
	}
	return dashKeys[b], evNone
}
