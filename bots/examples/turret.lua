-- turret: walks to the zone centre and shoots in a rotating pattern.
-- Keeps state between ticks in an upvalue.
local n = 0
local shots = { "shoot_n", "shoot_e", "shoot_s", "shoot_w" }

function act(obs)
  n = n + 1
  if math.abs(obs.zone_dx) > 1 then return obs.zone_dx > 0 and "move_w" or "move_e" end
  if math.abs(obs.zone_dy) > 1 then return obs.zone_dy > 0 and "move_n" or "move_s" end
  if obs.ammo > 0 and obs.shoot_cd == 0 then return shots[n % 4 + 1] end
  return "stay"
end
