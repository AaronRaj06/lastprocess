// Package sandbox runs untrusted Lua bots. Every script is treated as hostile:
// it gets its own VM, a whitelisted environment, a deterministic CPU budget and
// an allocation budget per tick. See docs/decisions.md (D10) for why golua.
package sandbox

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"

	"github.com/arnodel/golua/lib"
	"github.com/arnodel/golua/lib/base"
	"github.com/arnodel/golua/lib/mathlib"
	"github.com/arnodel/golua/lib/packagelib"
	"github.com/arnodel/golua/lib/stringlib"
	"github.com/arnodel/golua/lib/tablelib"
	rt "github.com/arnodel/golua/runtime"

	"github.com/AaronRaj06/lastprocess/internal/world"
)

// Limits. CPU is in golua VM ticks (deterministic, not wall-clock); memory is
// bytes allocated, counted as if the GC never ran.
const (
	MaxScriptSize = 16 << 10
	LoadCPU       = 200_000 // budget for running the script body once
	LoadMem       = 1 << 20 //
	TickCPU       = 20_000  // budget for one act() call
	TickMem       = 256 << 10
	MatchMem      = 16 << 20 // total allocation over a match
	MaxViolations = 10       // then the bot is disqualified and only no-ops
)

// The script may use only these globals (plus rand, and string/table/math
// with their unsafe members refused by golua's compliance flags).
var allowed = []string{
	"assert", "error", "ipairs", "next", "pairs", "pcall", "select",
	"tonumber", "tostring", "type", "string", "table", "math",
}

const flags = rt.ComplyCpuSafe | rt.ComplyMemSafe | rt.ComplyIoSafe | rt.ComplyTimeSafe

// Bot is one sandboxed script. It implements bot.Controller.
type Bot struct {
	r   *rt.Runtime
	act rt.Value
	mem uint64 // total allocation so far

	Violations   int
	Disqualified bool
	LastErr      string // most recent violation, for the owner to debug
	LastCPU      uint64 // CPU ticks used by the most recent act() call
}

var facingNames = [4]string{"n", "e", "s", "w"}

// New compiles script and runs its body once under the load budget. The script
// must define a global function act(obs). seed drives the script's rand().
func New(script string, seed uint64) (b *Bot, err error) {
	if len(script) > MaxScriptSize {
		return nil, fmt.Errorf("script is %d bytes, limit is %d", len(script), MaxScriptSize)
	}
	defer func() {
		if p := recover(); p != nil {
			b, err = nil, fmt.Errorf("script crashed the VM: %v", p)
		}
	}()
	r := rt.New(io.Discard)
	// packagelib must be loaded for the others to register; the script's
	// environment below never sees `package` or `require`.
	lib.LoadLibs(r, packagelib.LibLoader, base.LibLoader, stringlib.LibLoader, tablelib.LibLoader, mathlib.LibLoader)
	global := r.GlobalEnv()
	env := rt.NewTable()
	for _, name := range allowed {
		env.Set(rt.StringValue(name), global.Get(rt.StringValue(name)))
	}
	if m, ok := env.Get(rt.StringValue("math")).TryTable(); ok {
		// Unseeded randomness would break replays; rand() below replaces it.
		m.Set(rt.StringValue("random"), rt.NilValue)
		m.Set(rt.StringValue("randomseed"), rt.NilValue)
	}
	if m, ok := env.Get(rt.StringValue("string")).TryTable(); ok {
		m.Set(rt.StringValue("dump"), rt.NilValue) // bytecode is useless without load; remove it anyway
	}
	rng := rand.New(rand.NewPCG(seed, 0x10A))
	randFn := rt.NewGoFunction(func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if c.NArgs() == 0 {
			return c.PushingNext1(t.Runtime, rt.FloatValue(rng.Float64())), nil
		}
		n, err := c.IntArg(0)
		if err != nil || n < 1 {
			return nil, errors.New("rand(n): n must be a positive integer")
		}
		return c.PushingNext1(t.Runtime, rt.IntValue(1+rng.Int64N(n))), nil
	}, "rand", 1, false)
	randFn.SolemnlyDeclareCompliance(flags)
	env.Set(rt.StringValue("rand"), rt.FunctionValue(randFn))

	chunk, err := r.CompileAndLoadLuaChunk("bot", []byte(script), rt.TableValue(env))
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	b = &Bot{r: r}
	if _, err := b.call(LoadCPU, LoadMem, rt.FunctionValue(chunk)); err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	b.act = env.Get(rt.StringValue("act"))
	if _, ok := b.act.TryCallable(); !ok {
		return nil, errors.New("script must define a global function act(obs)")
	}
	return b, nil
}

// Check reports whether script would be accepted by New.
func Check(script string) error {
	_, err := New(script, 0)
	return err
}

// call runs f(args...) in a fresh quota context and reports why it failed, if it did.
func (b *Bot) call(cpu, mem uint64, f rt.Value, args ...rt.Value) (out rt.Value, err error) {
	defer func() {
		// golua turns quota breaches into a killed context; anything else that
		// panics inside the VM must still never take the server down.
		if p := recover(); p != nil {
			err = fmt.Errorf("vm panic: %v", p)
		}
	}()
	th := b.r.MainThread()
	ctx, err := th.CallContext(rt.RuntimeContextDef{
		HardLimits:    rt.RuntimeResources{Cpu: cpu, Memory: mem},
		RequiredFlags: flags,
	}, func() (e error) {
		out, e = rt.Call1(th, f, args...)
		return e
	})
	used := ctx.UsedResources()
	b.LastCPU = used.Cpu
	b.mem += used.Memory
	switch {
	case ctx.Status() == rt.StatusKilled:
		return rt.NilValue, fmt.Errorf("killed: budget exceeded (cpu %d/%d, mem %d/%d)", used.Cpu, cpu, used.Memory, mem)
	case err != nil:
		return rt.NilValue, err
	case b.mem > MatchMem:
		return rt.NilValue, fmt.Errorf("match allocation budget exceeded (%d bytes)", b.mem)
	}
	return out, nil
}

// Act implements bot.Controller. Any error, budget breach or bad return value
// is a violation and yields a no-op.
func (b *Bot) Act(o *world.Observation) world.Action {
	if b.Disqualified || !o.Alive {
		return world.Stay
	}
	out, err := b.call(TickCPU, TickMem, b.act, obsTable(o))
	if err == nil {
		if s, ok := out.TryString(); ok {
			if a, ok := world.ParseAction(s); ok {
				return a
			}
		}
		if n, ok := rt.ToIntNoString(out); ok && n >= 0 && n < int64(world.NumActions) {
			return world.Action(n)
		}
		err = fmt.Errorf("act returned %s, want an action name or 0..%d", out.TypeName(), world.NumActions-1)
	}
	b.Violations++
	b.LastErr = err.Error()
	// Running out of the match-wide budget cannot be recovered from.
	b.Disqualified = b.Violations >= MaxViolations || b.mem > MatchMem
	return world.Stay
}

// obsTable builds a fresh table every tick, so nothing the script does to it
// can reach the sim or a later tick. It is built by Go and not charged to the bot.
func obsTable(o *world.Observation) rt.Value {
	t := rt.NewTableWithCapacity(0, 12)
	set := func(k string, v int) { t.Set(rt.StringValue(k), rt.IntValue(int64(v))) }
	set("hp", o.HP)
	set("ammo", o.Ammo)
	set("shield", o.Shield)
	set("shoot_cd", o.ShootCD)
	set("dash_cd", o.DashCD)
	set("zone_dx", o.ZoneDX)
	set("zone_dy", o.ZoneDY)
	set("zone_dist", o.ZoneDist)
	set("shrink_in", o.ShrinkIn)
	t.Set(rt.StringValue("facing"), rt.StringValue(facingNames[o.Facing]))
	threats := rt.NewTableWithCapacity(0, 4)
	for d, dist := range o.Threats {
		threats.Set(rt.StringValue(facingNames[d]), rt.IntValue(int64(dist)))
	}
	t.Set(rt.StringValue("threats"), rt.TableValue(threats))
	view := rt.NewTableWithCapacity(len(o.View), 0)
	for i, c := range o.View {
		view.Set(rt.IntValue(int64(i+1)), rt.IntValue(int64(c)))
	}
	t.Set(rt.StringValue("view"), rt.TableValue(view))
	enemies := rt.NewTable()
	for i := range o.NEnemies {
		e := rt.NewTable()
		e.Set(rt.StringValue("dx"), rt.IntValue(int64(o.Enemies[i].DX)))
		e.Set(rt.StringValue("dy"), rt.IntValue(int64(o.Enemies[i].DY)))
		e.Set(rt.StringValue("facing"), rt.StringValue(facingNames[o.Enemies[i].Facing]))
		enemies.Set(rt.IntValue(int64(i+1)), rt.TableValue(e))
	}
	t.Set(rt.StringValue("enemies"), rt.TableValue(enemies))
	return rt.TableValue(t)
}
