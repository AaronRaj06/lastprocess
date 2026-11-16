-- chaser: stay in the zone, line up with the nearest enemy, shoot when aligned.
local names = { n = "_n", e = "_e", s = "_s", w = "_w" }

local function dir(dx, dy)
  if math.abs(dx) >= math.abs(dy) then
    return dx > 0 and "e" or "w"
  end
  return dy > 0 and "s" or "n"
end

function act(obs)
  if obs.zone_dist <= 1 then
    return "move" .. names[dir(-obs.zone_dx, -obs.zone_dy)]
  end
  local e = obs.enemies[1]
  if e and obs.ammo > 0 then
    if e.dx == 0 or e.dy == 0 then
      if obs.shoot_cd > 0 then return "stay" end
      return "shoot" .. names[dir(e.dx, e.dy)]
    end
    if math.abs(e.dx) <= math.abs(e.dy) then
      return "move" .. names[dir(e.dx, 0)]
    end
    return "move" .. names[dir(0, e.dy)]
  end
  if obs.zone_dx == 0 and obs.zone_dy == 0 then return "stay" end
  return "move" .. names[dir(-obs.zone_dx, -obs.zone_dy)]
end
