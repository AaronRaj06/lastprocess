package match

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AaronRaj06/lastprocess/internal/bot"
	"github.com/AaronRaj06/lastprocess/internal/replay"
	"github.com/AaronRaj06/lastprocess/internal/sandbox"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

// Phase is where a live match is in its lobby -> running -> results cycle.
type Phase uint8

// Phases of a live match.
const (
	Lobby Phase = iota
	Running
	Results
)

// Player is a connected person. As a bot.Controller it hands the sim the last
// key pressed since the previous tick, so the sim treats it like any other bot.
type Player struct {
	Name      string
	Key       string // account key; empty for guests
	BotName   string // optional uploaded Lua bot entered alongside (or instead of) the player
	BotScript string
	Spectate  bool // do not take a human slot

	action atomic.Uint32
	acks   atomic.Uint32
}

// Press sets the action for the next tick; the last press before the tick wins.
func (p *Player) Press(a world.Action) { p.action.Store(uint32(a)) }

// Acks is how many of this player's inputs the server has applied.
func (p *Player) Acks() uint32 { return p.acks.Load() }

// Act implements bot.Controller.
func (p *Player) Act(*world.Observation) world.Action {
	a := world.Action(p.action.Swap(0))
	if a != world.Stay {
		p.acks.Add(1)
	}
	return a
}

func (p *Player) slots() int {
	n := 0
	if !p.Spectate {
		n++
	}
	if p.BotScript != "" {
		n++
	}
	return n
}

// SlotInfo describes one combatant to viewers.
type SlotInfo struct {
	Name, Kind string
	Owner      *Player // the human in this slot, or the owner of this Lua bot; nil for server bots
}

// State is what subscribers receive every tick. It is immutable once sent.
type State struct {
	Phase   Phase
	Left    int      // ticks left in Lobby or Results
	Waiting []string // Lobby: who is in
	View    world.View
	Slots   []SlotInfo
	Winner  int   // Results: winning slot or -1
	ID      int64 // Results: stored match ID, 0 if not stored
}

// Result is passed to Config.OnEnd when a match finishes.
type Result struct {
	Slots  []SlotInfo
	Winner int
	Ticks  int
	Log    *replay.Log
}

// Config tunes the Manager.
type Config struct {
	Size        int           // combatants per match
	Fill        []string      // bot specs that fill empty slots, cycled
	MaxMatches  int           // concurrent matches
	LobbyTicks  int           // countdown before a match
	ResultTicks int           // how long the result is shown
	Tick        time.Duration // 50 ms in production
	OnEnd       func(Result) int64
}

// Manager owns the live matches. One permanent match always runs, so there is
// always something to watch; more are created when players arrive.
type Manager struct {
	cfg     Config
	mu      sync.Mutex
	matches []*Match
	seed    atomic.Uint64
	stats   tickStats
}

// Sub is one viewer's subscription to a match. C always holds the newest state:
// a slow reader skips frames instead of slowing the match down.
type Sub struct {
	C chan *State
	P *Player // nil for pure spectators
	m *Match
}

// Match is one live match loop.
type Match struct {
	mg        *Manager
	permanent bool
	mu        sync.Mutex
	phase     Phase
	subs      []*Sub
	closed    bool
}

// NewManager starts the permanent match.
func NewManager(cfg Config) *Manager {
	mg := &Manager{cfg: cfg}
	mg.seed.Store(uint64(time.Now().UnixNano()))
	mg.mu.Lock()
	mg.newMatch(true)
	mg.mu.Unlock()
	return mg
}

func (mg *Manager) newMatch(permanent bool) *Match {
	m := &Match{mg: mg, permanent: permanent}
	mg.matches = append(mg.matches, m)
	go m.run()
	return m
}

// Join puts p (and p's bot, if any) into a match that is still in its lobby,
// creating a match if none has room.
func (mg *Manager) Join(p *Player) (*Sub, error) {
	if p.slots() == 0 {
		return mg.Watch(), nil
	}
	mg.mu.Lock()
	defer mg.mu.Unlock()
	for _, m := range mg.matches {
		if s := m.tryJoin(p); s != nil {
			return s, nil
		}
	}
	if len(mg.matches) >= mg.cfg.MaxMatches {
		return nil, errors.New("server is full, try again in a minute")
	}
	return mg.newMatch(false).tryJoin(p), nil
}

// Watch subscribes to the busiest match without taking a slot.
func (mg *Manager) Watch() *Sub {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	best, bestN := mg.matches[0], -1
	for _, m := range mg.matches {
		m.mu.Lock()
		if n := len(m.subs); n > bestN && !m.closed {
			best, bestN = m, n
		}
		m.mu.Unlock()
	}
	s := &Sub{C: make(chan *State, 1), m: best}
	best.mu.Lock()
	best.subs = append(best.subs, s)
	best.mu.Unlock()
	return s
}

func (m *Match) tryJoin(p *Player) *Sub {
	m.mu.Lock()
	defer m.mu.Unlock()
	used := 0
	for _, s := range m.subs {
		if s.P != nil {
			used += s.P.slots()
		}
	}
	if m.closed || m.phase != Lobby || used+p.slots() > m.mg.cfg.Size {
		return nil
	}
	s := &Sub{C: make(chan *State, 1), P: p, m: m}
	m.subs = append(m.subs, s)
	return s
}

// Leave ends the subscription. A player who leaves mid-match simply stops sending input.
func (s *Sub) Leave() {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if i := slices.Index(s.m.subs, s); i >= 0 {
		s.m.subs = slices.Delete(s.m.subs, i, i+1)
	}
}

func (m *Match) broadcast(st *State) (viewers int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.subs {
		select {
		case <-s.C: // drop the frame the viewer has not read yet
		default:
		}
		s.C <- st // cannot block: we are the only sender and the buffer is now empty
	}
	return len(m.subs)
}

func (m *Match) setPhase(p Phase) {
	m.mu.Lock()
	m.phase = p
	m.mu.Unlock()
}

// start builds the game from whoever is in the lobby and fills the rest with bots.
func (m *Match) start() (*Game, []SlotInfo) {
	cfg := &m.mg.cfg
	seed := m.mg.seed.Add(0x9E3779B97F4A7C15)
	m.mu.Lock()
	m.phase = Running
	var slots []Slot
	var info []SlotInfo
	add := func(s Slot, owner *Player) {
		slots = append(slots, s)
		info = append(info, SlotInfo{s.Name, s.Kind, owner})
	}
	for _, s := range m.subs {
		p := s.P
		if p == nil {
			continue
		}
		if !p.Spectate {
			p.action.Store(0)
			add(Slot{p.Name, "human", p}, p)
		}
		if p.BotScript != "" {
			// Scripts are validated at upload; a failure here just leaves the slot to a server bot.
			if b, err := sandbox.New(p.BotScript, seed+uint64(len(slots))); err == nil {
				add(Slot{p.Name + "/" + p.BotName, "lua", b}, p)
			}
		}
	}
	m.mu.Unlock()
	for i := 0; len(slots) < cfg.Size; i++ {
		spec := cfg.Fill[i%len(cfg.Fill)]
		ctl, err := NewController(spec, seed*31+uint64(len(slots)))
		if err != nil {
			ctl, spec = bot.NewBaseline(seed), "baseline"
		}
		kind := kindOf(spec)
		add(Slot{kind + "-" + string(rune('a'+len(slots))), kind, ctl}, nil)
	}
	return NewGame(seed, slots), info
}

// wait blocks until the next tick and records how late it fired.
func (m *Match) wait(t *time.Ticker, last *time.Time) {
	now := <-t.C
	if !last.IsZero() {
		m.mg.stats.late(now.Sub(*last) - m.mg.cfg.Tick)
	}
	*last = now
}

func (m *Match) run() {
	cfg := &m.mg.cfg
	t := time.NewTicker(cfg.Tick)
	defer t.Stop()
	var last time.Time
	for {
		for left := cfg.LobbyTicks; left > 0; left-- {
			m.wait(t, &last)
			st := &State{Phase: Lobby, Left: left}
			m.mu.Lock()
			for _, s := range m.subs {
				if s.P != nil {
					st.Waiting = append(st.Waiting, s.P.Name)
				}
			}
			m.mu.Unlock()
			m.broadcast(st)
		}
		g, info := m.start()
		for !g.Sim.Done() {
			m.wait(t, &last)
			begin := time.Now()
			g.Tick()
			viewers := m.broadcast(&State{Phase: Running, View: g.Sim.Snapshot(), Slots: info, Winner: -1})
			m.mg.stats.work(time.Since(begin))
			if viewers == 0 && !m.permanent {
				break // everyone left; nobody to finish the match for
			}
		}
		if g.Sim.Done() {
			res := Result{Slots: info, Winner: g.Winner(), Ticks: int(g.Sim.Tick), Log: &g.Log}
			var id int64
			if cfg.OnEnd != nil {
				id = cfg.OnEnd(res)
			}
			final := g.Sim.Snapshot()
			m.setPhase(Results)
			for left := cfg.ResultTicks; left > 0; left-- {
				m.wait(t, &last)
				m.broadcast(&State{Phase: Results, Left: left, View: final, Slots: info, Winner: res.Winner, ID: id})
			}
		}
		// Lock order is always manager, then match.
		m.mg.mu.Lock()
		m.mu.Lock()
		m.phase = Lobby
		if len(m.subs) == 0 && !m.permanent {
			m.closed = true
			m.mg.matches = slices.DeleteFunc(m.mg.matches, func(o *Match) bool { return o == m })
		}
		closed := m.closed
		m.mu.Unlock()
		m.mg.mu.Unlock()
		if closed {
			return
		}
	}
}

// Stats is a snapshot of server load. Durations are in microseconds.
type Stats struct {
	Matches, Viewers          int
	Ticks                     int
	WorkP50, WorkP99, WorkMax int64 // time to step the sim and fan out one tick
	LateP50, LateP99, LateMax int64 // how late ticks fired against the 20 Hz schedule
	LateTicks                 int   // ticks more than 5 ms late
}

// Stats reports load since the last call with reset.
func (mg *Manager) Stats(reset bool) Stats {
	mg.mu.Lock()
	s := Stats{Matches: len(mg.matches)}
	for _, m := range mg.matches {
		m.mu.Lock()
		s.Viewers += len(m.subs)
		m.mu.Unlock()
	}
	mg.mu.Unlock()
	mg.stats.fill(&s, reset)
	return s
}

// tickStats keeps the most recent samples in fixed rings.
type tickStats struct {
	mu           sync.Mutex
	works        [1 << 15]time.Duration
	lates        [1 << 15]time.Duration
	nWork, nLate int
	lateTicks    int
}

func (t *tickStats) work(d time.Duration) {
	t.mu.Lock()
	t.works[t.nWork%len(t.works)] = d
	t.nWork++
	t.mu.Unlock()
}

func (t *tickStats) late(d time.Duration) {
	t.mu.Lock()
	t.lates[t.nLate%len(t.lates)] = max(d, -d)
	t.nLate++
	if d > 5*time.Millisecond {
		t.lateTicks++
	}
	t.mu.Unlock()
}

func (t *tickStats) fill(s *Stats, reset bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pct := func(ring []time.Duration, n int) (p50, p99, mx int64) {
		v := slices.Clone(ring[:min(n, len(ring))])
		if len(v) == 0 {
			return
		}
		slices.Sort(v)
		return v[len(v)/2].Microseconds(), v[len(v)*99/100].Microseconds(), v[len(v)-1].Microseconds()
	}
	s.Ticks, s.LateTicks = t.nLate, t.lateTicks
	s.WorkP50, s.WorkP99, s.WorkMax = pct(t.works[:], t.nWork)
	s.LateP50, s.LateP99, s.LateMax = pct(t.lates[:], t.nLate)
	if reset {
		t.nWork, t.nLate, t.lateTicks = 0, 0, 0
	}
}
