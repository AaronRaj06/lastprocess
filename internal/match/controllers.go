package match

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/AaronRaj06/lastprocess/internal/bot"
	"github.com/AaronRaj06/lastprocess/internal/sandbox"
)

// NewController builds a bot from a spec: "random", "baseline", "rl" (the
// embedded policy), "rl:<policy.onnx>" or "lua:<script.lua>". seed drives the bot's own RNG.
func NewController(spec string, seed uint64) (bot.Controller, error) {
	kind, arg, _ := strings.Cut(spec, ":")
	switch kind {
	case "random":
		return bot.NewRandom(seed), nil
	case "baseline":
		return bot.NewBaseline(seed), nil
	case "rl":
		p, err := policy(arg)
		if err != nil {
			return nil, err
		}
		return p.Controller(), nil
	case "lua":
		src, err := os.ReadFile(arg)
		if err != nil {
			return nil, err
		}
		return sandbox.New(string(src), seed)
	}
	return nil, fmt.Errorf("unknown bot %q", spec)
}

var policies sync.Map // path -> *bot.Policy; weights are immutable, so one copy serves every match

// policy returns the embedded policy for "" or a cached policy loaded from path.
func policy(path string) (*bot.Policy, error) {
	if p, ok := policies.Load(path); ok {
		return p.(*bot.Policy), nil
	}
	var p *bot.Policy
	var err error
	if path == "" {
		p, err = bot.EmbeddedPolicy()
	} else {
		p, err = bot.LoadPolicy(path)
	}
	if err != nil {
		return nil, fmt.Errorf("rl policy: %w", err)
	}
	policies.Store(path, p)
	return p, nil
}

func kindOf(spec string) string {
	kind, _, _ := strings.Cut(spec, ":")
	return kind
}

// ParseLineup expands "baseline,random*7" into one spec per slot.
func ParseLineup(s string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(s, ",") {
		spec, count, has := strings.Cut(strings.TrimSpace(part), "*")
		n := 1
		if has {
			var err error
			if n, err = strconv.Atoi(count); err != nil || n < 1 {
				return nil, fmt.Errorf("bad count in %q", part)
			}
		}
		if _, err := NewController(spec, 0); err != nil {
			return nil, err
		}
		for range n {
			out = append(out, spec)
		}
	}
	if len(out) < 2 || len(out) > 16 {
		return nil, fmt.Errorf("need 2 to 16 combatants, got %d", len(out))
	}
	return out, nil
}

// NewGameFromLineup builds a game from specs; bot i gets a seed derived from the match seed.
func NewGameFromLineup(seed uint64, lineup []string) (*Game, error) {
	slots := make([]Slot, len(lineup))
	for i, spec := range lineup {
		ctl, err := NewController(spec, seed*31+uint64(i))
		if err != nil {
			return nil, err
		}
		kind := kindOf(spec)
		slots[i] = Slot{Name: fmt.Sprintf("%s-%d", kind, i), Kind: kind, Ctl: ctl}
	}
	return NewGame(seed, slots), nil
}
