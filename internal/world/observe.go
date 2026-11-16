package world

import "github.com/AaronRaj06/lastprocess/internal/ecs"

// The bot observation. docs/bot-api.md is the contract; keep the two in sync.
const (
	ViewSize   = 15 // the local view is ViewSize x ViewSize cells centred on the bot
	viewR      = ViewSize / 2
	MaxEnemies = 3
	// ObsLen is the length of the flat vector used by RL policies.
	ObsLen = ViewSize*ViewSize + 10 + MaxEnemies*4
)

// Cell codes in Observation.View.
const (
	CellFloor uint8 = iota
	CellWall
	CellHealth
	CellAmmo
	CellShield
	CellProjectile
	CellEnemy
)

// EnemyObs is one visible enemy, relative to the observer.
type EnemyObs struct {
	DX, DY int
	Facing Dir
}

// Observation is everything a bot may know for one tick. RL, Lua and native
// bots all receive exactly this and return one Action.
type Observation struct {
	View             [ViewSize * ViewSize]uint8 // row-major; index (dy+7)*15 + (dx+7)
	HP, Ammo, Shield int
	ShootCD, DashCD  int
	Facing           Dir
	ZoneDX, ZoneDY   int                  // own position minus zone centre
	ZoneDist         int                  // cells to the nearest zone edge; negative when outside
	ShrinkIn         int                  // ticks until the zone shrinks; 0 once it has stopped
	Enemies          [MaxEnemies]EnemyObs // nearest first (Manhattan), only those inside the view
	NEnemies         int
	Alive            bool
}

// Observe fills o for combatant e. It must be called between Steps.
func (s *Sim) Observe(e ecs.Entity, o *Observation) {
	*o = Observation{}
	c, ok := s.combatants.Get(e)
	if !ok || c.HP <= 0 {
		return
	}
	me, _ := s.pos.Get(e)
	o.Alive = true
	o.HP, o.Ammo, o.Shield, o.ShootCD, o.DashCD, o.Facing = c.HP, c.Ammo, c.Shield, c.ShootCD, c.DashCD, c.Facing
	z := s.Zone()
	o.ZoneDX, o.ZoneDY = me.X-(z.X0+z.X1)/2, me.Y-(z.Y0+z.Y1)/2
	o.ZoneDist = min(me.X-z.X0, z.X1-me.X, me.Y-z.Y0, z.Y1-me.Y)
	o.ShrinkIn = s.ShrinkIn()

	for dy := -viewR; dy <= viewR; dy++ {
		for dx := -viewR; dx <= viewR; dx++ {
			if s.Map.At(me.X+dx, me.Y+dy) == Wall {
				o.View[(dy+viewR)*ViewSize+dx+viewR] = CellWall
			}
		}
	}
	mark := func(p *Position, code uint8) (dx, dy int, ok bool) {
		dx, dy = p.X-me.X, p.Y-me.Y
		if dx < -viewR || dx > viewR || dy < -viewR || dy > viewR {
			return 0, 0, false
		}
		o.View[(dy+viewR)*ViewSize+dx+viewR] = code
		return dx, dy, true
	}
	s.pickups.Each(func(pe ecs.Entity, k *PickupKind) {
		p, _ := s.pos.Get(pe)
		mark(p, CellHealth+uint8(*k))
	})
	s.projectiles.Each(func(pe ecs.Entity, _ *Projectile) {
		p, _ := s.pos.Get(pe)
		mark(p, CellProjectile)
	})
	s.combatants.Each(func(oe ecs.Entity, oc *Combatant) {
		if oe == e || oc.HP <= 0 {
			return
		}
		p, _ := s.pos.Get(oe)
		dx, dy, ok := mark(p, CellEnemy)
		if !ok {
			return
		}
		// Insert by distance; ascending-ID iteration breaks ties.
		en, d := EnemyObs{dx, dy, oc.Facing}, abs(dx)+abs(dy)
		i := o.NEnemies
		for i > 0 && abs(o.Enemies[i-1].DX)+abs(o.Enemies[i-1].DY) > d {
			i--
		}
		if i >= MaxEnemies {
			return
		}
		copy(o.Enemies[i+1:], o.Enemies[i:])
		o.Enemies[i] = en
		o.NEnemies = min(o.NEnemies+1, MaxEnemies)
	})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Vector writes the observation as ObsLen floats, roughly in [-1, 1], for RL policies.
// Layout: 225 view cells, 10 self features, then 3 x (present, dx, dy, facing).
func (o *Observation) Vector(dst []float32) {
	_ = dst[ObsLen-1]
	for i, c := range o.View {
		dst[i] = float32(c) / float32(CellEnemy)
	}
	self := [10]float32{
		float32(o.HP) / StartHP, float32(o.Ammo) / MaxAmmo, float32(o.Shield) / MaxShield,
		float32(o.ShootCD) / ShootCooldown, float32(o.DashCD) / DashCooldown, float32(o.Facing) / 3,
		float32(o.ZoneDX) / 40, float32(o.ZoneDY) / 20, float32(o.ZoneDist) / 20, float32(o.ShrinkIn) / ShrinkEvery,
	}
	n := copy(dst[len(o.View):], self[:]) + len(o.View)
	for i := range MaxEnemies {
		e := [4]float32{}
		if i < o.NEnemies {
			e = [4]float32{1, float32(o.Enemies[i].DX) / viewR, float32(o.Enemies[i].DY) / viewR, float32(o.Enemies[i].Facing) / 3}
		}
		n += copy(dst[n:], e[:])
	}
}
