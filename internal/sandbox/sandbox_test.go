package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AaronRaj06/lastprocess/internal/world"
)

func liveObs() *world.Observation {
	return &world.Observation{Alive: true, HP: 100, Ammo: 10, NEnemies: 1, Enemies: [3]world.EnemyObs{{DX: 2, DY: 0}}}
}

// Every attack must be contained: the script is either rejected at load, or
// each act() call yields a no-op and a violation. Nothing may hang, crash the
// process or grow the heap without bound.
var attacks = []struct{ name, src string }{
	{"infinite loop", `function act() while true do end end`},
	{"infinite loop at load", `while true do end function act() end`},
	{"infinite loop hidden in pcall", `function act() while true do pcall(function() while true do end end) end end`},
	{"goto loop", `function act() ::top:: goto top end`},
	{"table memory bomb", `t = {} function act() while true do t[#t+1] = {} end end`},
	{"string doubling bomb", `s = "x" function act() while true do s = s .. s end end`},
	{"string.rep bomb", `function act() return string.rep("x", 1000000000) end`},
	{"string.rep method bomb", `function act() return ("x"):rep(1000000000) end`},
	{"string.format width bomb", `function act() return string.format("%99999999d", 1) end`},
	{"table.concat bomb", `function act() local t = {} for i = 1, 1e9 do t[i] = "xxxxxxxx" end return table.concat(t) end`},
	{"unpack bomb", `function act() return select("#", table.unpack({}, 1, 100000000)) end`},
	{"deep recursion", `local function f() return 1 + f() end function act() return f() end`},
	{"pcall recursion", `local function f() pcall(f) end function act() f() end`},
	{"tostring metamethod recursion", `function act(o) local s = "" for i = 1, 1e9 do s = tostring(s) .. "x" end end`},
	{"pattern backtracking bomb", `function act() return string.find(string.rep("a", 40), string.rep("a*", 40) .. "b") end`},
	{"gsub expansion bomb", `function act() local s = "aaaaaaaa" for i = 1, 64 do s = string.gsub(s, "a", "aaaaaaaa") end return s end`},
	{"growth across ticks", `t = {} function act() for i = 1, 200 do t[#t+1] = "0123456789abcdef" .. i .. #t end return "stay" end`},
	{"error", `function act() error("boom") end`},
	{"error with table", `function act() error({}) end`},
	{"error in error handler", `function act() error(setmetatable and 1 or nil) end`},
	{"huge return string", `function act() return string.rep("a", 100000) end`},
	{"table return", `function act() return {1, 2, 3} end`},
	{"out of range return", `function act() return 99 end`},
	{"negative return", `function act() return -1 end`},
	{"float return", `function act() return 1.5 end`},
	{"no return", `function act() end`},
	{"function return", `function act() return act end`},
	{"unknown action name", `function act() return "self_destruct" end`},
}

func TestAttacksContained(t *testing.T) {
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for _, a := range attacks {
		t.Run(a.name, func(t *testing.T) {
			start := time.Now()
			b, err := New(a.src, 1)
			if (err != nil) != (a.name == "infinite loop at load") {
				t.Fatalf("load: %v", err)
			}
			if err == nil {
				for i := range 30 {
					act := b.Act(liveObs())
					if a.name == "growth across ticks" {
						for !b.Disqualified && i == 29 {
							b.Act(liveObs())
						}
						continue
					}
					if act != world.Stay || b.Violations != min(i+1, MaxViolations) {
						t.Fatalf("tick %d: action %v, violations %d, last %q", i, act, b.Violations, b.LastErr)
					}
				}
				if !b.Disqualified {
					t.Fatal("not disqualified after repeated violations")
				}
				t.Logf("contained in act: %s", b.LastErr)
			} else {
				t.Logf("rejected at load: %v", err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("took %v", d)
			}
		})
	}
	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)
	if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 64<<20 {
		t.Fatalf("heap grew by %d MB", grown>>20)
	}
	t.Logf("%d attack cases contained", len(attacks))
}

// Nothing that touches the filesystem, network, clock, process or loader may be reachable.
func TestBlockedGlobals(t *testing.T) {
	for _, name := range []string{
		"os", "io", "debug", "package", "require", "load", "loadfile", "loadstring", "dofile",
		"collectgarbage", "print", "warn", "coroutine", "utf8", "golib", "runtime",
		"getmetatable", "setmetatable", "rawget", "rawset", "rawequal", "rawlen", "_G", "_ENV.os",
		"math.random", "math.randomseed", "string.dump",
	} {
		b, err := New(`function act() if `+name+` == nil then return "move_n" end return "stay" end`, 1)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if b.Act(liveObs()) != world.MoveN {
			t.Errorf("%s is reachable (%s)", name, b.LastErr)
		}
	}
}

func TestRejectedAtLoad(t *testing.T) {
	for name, src := range map[string]string{
		"syntax error": `function act( return end`,
		"no act":       `x = 1`,
		"act not func": `act = 5`,
		"too large":    `function act() end --` + strings.Repeat("x", MaxScriptSize),
		"load error":   `error("at load") function act() end`,
	} {
		if err := Check(src); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A bot can change only its own copy of the observation, and two bots share nothing.
func TestIsolation(t *testing.T) {
	a, err := New(`secret = 42 string.leak = 1 function act(o) o.hp = 9999 o.view[1] = 42 o.enemies[1].dx = 0 return "stay" end`, 1)
	if err != nil {
		t.Fatal(err)
	}
	o := liveObs()
	a.Act(o)
	if o.HP != 100 || o.View[0] != 0 || o.Enemies[0].DX != 2 {
		t.Fatalf("script changed the sim's observation: %+v", o)
	}
	b, err := New(`function act(o) if secret == nil and string.leak == nil and o.hp == 100 then return "move_e" end return "stay" end`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if b.Act(o) != world.MoveE {
		t.Fatal("state leaked between bots")
	}
	c, _ := New(`function act(o) if o.hp == 100 and o.view[1] == 0 then return "move_s" end return "stay" end`, 1)
	c.Act(o)
	if c.Act(o) != world.MoveS {
		t.Fatal("observation not rebuilt each tick")
	}
}

// The CPU budget counts VM ticks, not time, so the same script with the same
// seed is cut off at exactly the same point every run.
func TestDeterministicBudgetAndRand(t *testing.T) {
	const src = `
local n = 0
function act(o)
  local spins = rand(40000)          -- sometimes over budget, sometimes not
  for i = 1, spins do n = n + 1 end
  return rand(13) - 1
end`
	run := func() (acts []world.Action, cpu []uint64, viol int) {
		b, err := New(src, 7)
		if err != nil {
			t.Fatal(err)
		}
		b.Violations = -1000 // never disqualify in this test
		for range 200 {
			acts = append(acts, b.Act(liveObs()))
			cpu = append(cpu, b.LastCPU)
		}
		return acts, cpu, b.Violations + 1000
	}
	a1, c1, v1 := run()
	a2, c2, v2 := run()
	if b, _ := New(src, 7); v1 == 200 {
		b.Act(liveObs())
		t.Log(b.LastErr)
	}
	if v1 == 0 || v1 == 200 {
		t.Fatalf("want a mix of in-budget and over-budget ticks, got %d violations", v1)
	}
	for i := range a1 {
		if a1[i] != a2[i] || c1[i] != c2[i] {
			t.Fatalf("tick %d diverged: %v/%d vs %v/%d", i, a1[i], c1[i], a2[i], c2[i])
		}
	}
	if v1 != v2 {
		t.Fatalf("violations %d vs %d", v1, v2)
	}
}

func TestExampleBotsRunClean(t *testing.T) {
	files, _ := filepath.Glob("../../bots/examples/*.lua")
	if len(files) < 3 {
		t.Fatal("example bots missing")
	}
	for _, f := range files {
		src, _ := os.ReadFile(f)
		b, err := New(string(src), 3)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var maxCPU uint64
		for range world.MaxTicks {
			b.Act(liveObs())
			maxCPU = max(maxCPU, b.LastCPU)
		}
		if b.Violations != 0 {
			t.Fatalf("%s: %d violations: %s", f, b.Violations, b.LastErr)
		}
		t.Logf("%s: max %d cpu ticks per act (budget %d), %d KB allocated over a full match (budget %d KB)",
			filepath.Base(f), maxCPU, TickCPU, b.mem>>10, MatchMem>>10)
	}
}

func BenchmarkLuaAct(b *testing.B) {
	src, _ := os.ReadFile("../../bots/examples/chaser.lua")
	bot, err := New(string(src), 1)
	if err != nil {
		b.Fatal(err)
	}
	bot.mem = 0
	o := liveObs()
	for b.Loop() {
		bot.Act(o)
		bot.mem = 0 // the benchmark runs far longer than a match
	}
	if bot.Violations != 0 {
		b.Fatal(bot.LastErr)
	}
}
