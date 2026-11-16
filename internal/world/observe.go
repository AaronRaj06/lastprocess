package world

import "github.com/AaronRaj06/lastprocess/internal/ecs"

// The bot observation. docs/bot-api.md is the contract; keep the two in sync.
const (
	ViewSize   = 15 // the local view is ViewSize x ViewSize cells centred on the bot
	viewR      = ViewSize / 2
	MaxEnemies = 3
	// ObsLen is the length of the flat vector used by RL policies.
	ObsLen = ViewSize*ViewSize + 10 + MaxEnemies*4 + 4*3
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
	Threats          [4]int // per direction N,E,S,W: distance to the nearest enemy projectile flying at you on that line; 0 = none
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
	s.projectiles.Each(func(pe ecs.Entity, pr *Projectile) {
		p, _ := s.pos.Get(pe)
		dx, dy, ok := mark(p, CellProjectile)
		if !ok || pr.Owner == e || (dx != 0 && dy != 0) || (dx == 0 && dy == 0) {
			return
		}
		// from is the side the projectile is on; it threatens us if it flies the opposite way.
		from := North
		switch {
		case dx > 0:
			from = East
		case dx < 0:
			from = West
		case dy > 0:
			from = South
		}
		if d := abs(dx) + abs(dy); pr.Dir == (from+2)%4 && (o.Threats[from] == 0 || d < o.Threats[from]) {
			o.Threats[from] = d
		}
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
// Layout: 225 view cells, 10 self features, 3 x (present, dx, dy, facing), then
// for each direction N, E, S, W a ray cast through the view: (free cells before
// the first wall, closeness of the first enemy in the line of fire, closeness
// of an incoming enemy projectile, from Threats). The rays add no information; they
// restate the view in the form shooting and dodging decisions need.
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
	for d := range Dir(4) {
		dx, dy := d.Delta()
		free, enemy, shot := float32(viewR), float32(0), float32(0)
		for k := 1; k <= viewR; k++ {
			c := o.View[(dy*k+viewR)*ViewSize+dx*k+viewR]
			if c == CellWall {
				free = float32(k - 1)
				break
			}
			near := float32(viewR+1-k) / viewR
			if c == CellEnemy && enemy == 0 {
				enemy = near
			}
		}
		if t := o.Threats[d]; t > 0 && float32(t-1) <= free {
			shot = float32(viewR+1-t) / viewR
		}
		n += copy(dst[n:], []float32{free / viewR, enemy, shot})
	}
}
