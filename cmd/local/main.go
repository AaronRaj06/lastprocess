// Command local runs a single-player match in the terminal for eyeballing the sim.
// The fixed 20 Hz pacing lives here, not in the sim: world.Sim never reads the clock.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AaronRaj06/lastprocess/internal/ecs"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

type tickMsg struct{}

type model struct {
	sim     *world.Sim
	seed    uint64
	player  ecs.Entity
	pending world.Action // last key pressed this tick wins
}

var (
	playerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	wallStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

var keyActions = map[string]world.Action{
	"up": world.MoveN, "w": world.MoveN, "k": world.MoveN,
	"right": world.MoveE, "d": world.MoveE, "l": world.MoveE,
	"down": world.MoveS, "s": world.MoveS, "j": world.MoveS,
	"left": world.MoveW, "a": world.MoveW, "h": world.MoveW,
}

func tick() tea.Cmd {
	return tea.Tick(time.Second/world.TickRate, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Init() tea.Cmd { return tick() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if s := msg.String(); s == "q" || s == "ctrl+c" || s == "esc" {
			return m, tea.Quit
		} else if a, ok := keyActions[s]; ok {
			m.pending = a
		}
	case tickMsg:
		m.sim.Step([]world.Input{{Combatant: m.player, Action: m.pending}})
		m.pending = world.Stay
		return m, tick()
	}
	return m, nil
}

func (m model) View() string {
	v := m.sim.Snapshot()
	at := map[int]world.CombatantView{}
	var me world.CombatantView
	for _, c := range v.Combatants {
		at[c.Y*v.Map.W+c.X] = c
		if c.ID == m.player {
			me = c
		}
	}
	var b strings.Builder
	for y := range v.Map.H {
		for x := range v.Map.W {
			if c, ok := at[y*v.Map.W+x]; ok && c.ID == m.player {
				b.WriteString(playerStyle.Render("@"))
			} else if v.Map.At(x, y) == world.Wall {
				b.WriteString(wallStyle.Render("#"))
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "seed %d  tick %d  pos %d,%d  hp %d   move: arrows/wasd/hjkl  quit: q",
		m.seed, v.Tick, me.X, me.Y, me.HP)
	return b.String()
}

func main() {
	seed := flag.Uint64("seed", uint64(time.Now().UnixNano()), "map seed")
	flag.Parse()
	sim := world.New(world.Config{Seed: *seed})
	m := model{sim: sim, seed: *seed, player: sim.Spawn()}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
