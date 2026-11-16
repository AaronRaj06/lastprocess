# Bot API

Every combatant, whether a person, the RL policy, a Lua script or a native Go bot, is driven through one interface (`internal/bot`):

```go
type Controller interface {
    Act(o *world.Observation) world.Action
}
```

Once per tick (20 per second) each controller gets an observation and returns one action. The sim never learns what kind of controller it was.

## Actions

| Value | Name | Effect |
|---|---|---|
| 0 | `stay` | nothing |
| 1-4 | `move_n` `move_e` `move_s` `move_w` | step one cell and face that way; blocked by walls and other combatants |
| 5-8 | `shoot_n` `shoot_e` `shoot_s` `shoot_w` | fire a projectile; costs 1 ammo; 5-tick cooldown |
| 9-12 | `dash_n` `dash_e` `dash_s` `dash_w` | move up to 3 cells at once; 40-tick cooldown |

North is up (y decreases). An action that is not available (no ammo, cooling down, blocked) still turns the combatant to face that direction.

## Rules that matter to a bot

- 100 HP, 10 ammo (max 20), 0 shield (max 50) at spawn.
- A projectile flies 2 cells per tick in a straight line until it hits a wall or a combatant, and does 20 damage. Shield absorbs damage first. You cannot hit yourself.
- Pickups: health +30, ammo +5, shield +25. One appears every 2 seconds inside the zone, up to 12 on the map.
- The zone is a rectangle that shrinks every 3 seconds (60 ticks) by 2 columns and 1 row per side, down to about 12x6. Outside it you lose 1 HP per tick, and shield does not help.
- Last one alive wins. At 90 seconds (1800 ticks) the survivor with the most HP wins.
- All combatants act in the same tick. Moves are resolved in slot order, so on a contested cell the lower slot gets it.

## Observation

| Field | Meaning |
|---|---|
| `view` | 15x15 grid centred on you, row-major. Cell at offset (dx, dy), each from -7 to 7, is index `(dy+7)*15 + (dx+7)` (add 1 in Lua). |
| `hp`, `ammo`, `shield` | your stats |
| `shoot_cd`, `dash_cd` | ticks until you can shoot / dash again (0 = ready) |
| `facing` | `n`, `e`, `s` or `w` (0-3 in Go) |
| `zone_dx`, `zone_dy` | your position minus the zone centre |
| `zone_dist` | cells to the nearest zone edge; negative when you are outside |
| `shrink_in` | ticks until the zone shrinks again; 0 once it has stopped |
| `threats` | per direction (`n`, `e`, `s`, `w`; a 4-array in Go): distance to the nearest enemy projectile on that line that is flying towards you, or 0 if none |
| `enemies` | up to 3 nearest enemies **inside the view**, nearest first, each `{dx, dy, facing}` |

View cell codes: `0` floor, `1` wall, `2` health, `3` ammo, `4` shield, `5` projectile, `6` enemy. Anything outside the map reads as wall. There is no line-of-sight check: you see everything in the 15x15 window, and nothing beyond it.

## Lua bots

A script defines one global function. It is called once per tick with the observation as a table and returns an action name (or its number):

```lua
function act(obs)
  local e = obs.enemies[1]
  if e and e.dy == 0 and obs.ammo > 0 and obs.shoot_cd == 0 then
    return e.dx > 0 and "shoot_e" or "shoot_w"
  end
  return "move_n"
end
```

Globals and upvalues persist between ticks, so a bot can remember things. Starter bots are in `bots/examples`.

Available: `assert error ipairs next pairs pcall select tonumber tostring type`, the `string`, `table` and `math` libraries, and `rand()`. Nothing else exists: no `os`, `io`, `debug`, `package`, `require`, `load`, `loadfile`, `dofile`, `collectgarbage`, `print`, `coroutine`, metatable functions or `math.random`.

`rand()` returns a float in [0, 1); `rand(n)` returns an integer in 1..n. It is seeded from the match seed.

### Limits

| Limit | Value | What happens |
|---|---|---|
| Script size | 16 KB | upload rejected |
| CPU for the script body (run once at load) | 200,000 VM ticks | upload rejected |
| CPU per `act` call | 20,000 VM ticks | no-op this tick, 1 violation |
| Memory allocated per `act` call | 256 KB | no-op this tick, 1 violation |
| Memory allocated over a whole match | 16 MB | disqualified |
| Runtime error, or a return value that is not an action | | no-op this tick, 1 violation |
| Violations in one match | 10 | disqualified: the bot only no-ops for the rest of the match |

CPU is counted in VM ticks, not time, so the same script is cut off at the same point on every machine. Memory counts bytes allocated, as if the garbage collector never ran; the example bots allocate under 12 KB over a full match. The observation table is rebuilt every tick; writing to it changes nothing.

### Uploading

Your account is your SSH public key.

```
ssh play.example.com upload-bot mybot < mybot.lua
ssh -t play.example.com play mybot       # you play, and your bot joins too
ssh -t play.example.com spectate mybot   # just watch your bot fight
```

Test locally first: `go run ./cmd/headless -bots 'lua:mybot.lua,baseline' -matches 200`.

## RL policies

`Observation.Vector` flattens the observation into 259 float32 values for neural policies: the 225 view cells (code / 6), 10 own features, 3 x (present, dx, dy, facing) for the enemies, then 4 rays (one per direction: free cells before the first wall, closeness of the first enemy in the line of fire, closeness of an incoming projectile from `threats`). The rays are computed from the fields above and add no information. The exact scaling is in `internal/world/observe.go`.
