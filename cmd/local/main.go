// Command local runs a single-player match against bots in the terminal, with
// no server. The fixed 20 Hz pacing lives here, not in the sim.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AaronRaj06/lastprocess/internal/match"
	"github.com/AaronRaj06/lastprocess/internal/render"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

type tickMsg struct{}

type model struct {
	game   *match.Game
	player *match.Player
	w, h   int
}

var keyActions = map[string]world.Action{
	"w": world.MoveN, "d": world.MoveE, "s": world.MoveS, "a": world.MoveW,
	"W": world.DashN, "D": world.DashE, "S": world.DashS, "A": world.DashW,
	"up": world.ShootN, "right": world.ShootE, "down": world.ShootS, "left": world.ShootW,
}

func tick() tea.Cmd {
	return tea.Tick(time.Second/world.TickRate, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Init() tea.Cmd { return tick() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyMsg:
		if s := msg.String(); s == "q" || s == "ctrl+c" || s == "esc" {
			return m, tea.Quit
		} else if a, ok := keyActions[s]; ok {
			m.player.Press(a)
		}
	case tickMsg:
		if !m.game.Sim.Done() {
			m.game.Tick()
		}
		return m, tick()
	}
	return m, nil
}

func (m model) View() string {
	if m.w == 0 {
		return ""
	}
	v := m.game.Sim.Snapshot()
	help := "wasd move  arrows shoot  WASD dash  q quit"
	if v.Done {
		help = "match over - q to quit"
	}
	var f render.Frame
	render.Draw(&f, &v, m.game.IDs[0], m.w, m.h, render.Status(&v, m.game.IDs[0]), help)
	return render.Text(&f)
}

func main() {
	seed := flag.Uint64("seed", uint64(time.Now().UnixNano()), "match seed")
	bots := flag.String("bots", "baseline*7", "opponents, as for cmd/headless")
	flag.Parse()
	lineup, err := match.ParseLineup("baseline," + *bots)
	if err == nil {
		var g *match.Game
		if g, err = match.NewGameFromLineup(*seed, lineup); err == nil {
			p := &match.Player{Name: "you"}
			g.Slots[0] = match.Slot{Name: "you", Kind: "human", Ctl: p}
			_, err = tea.NewProgram(model{game: g, player: p}, tea.WithAltScreen()).Run()
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
