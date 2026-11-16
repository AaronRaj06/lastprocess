-- wanderer: random walk that avoids walls, dashes back when outside the zone,
-- and fires at anything in line. Shows rand() and the view grid.
local moves = { "move_n", "move_e", "move_s", "move_w" }
local step = { { 0, -1 }, { 1, 0 }, { 0, 1 }, { -1, 0 } }

-- view is a 15x15 grid, row-major, 1-based; the bot is at the centre (8, 8).
local function cell(obs, dx, dy)
  return obs.view[(dy + 7) * 15 + (dx + 7) + 1]
end

function act(obs)
  if obs.zone_dist < 0 then
    local horizontal = math.abs(obs.zone_dx) > 2 * math.abs(obs.zone_dy)
    local d
    if horizontal then d = obs.zone_dx > 0 and "w" or "e" else d = obs.zone_dy > 0 and "n" or "s" end
    return (obs.dash_cd == 0 and "dash_" or "move_") .. d
  end
  local e = obs.enemies[1]
  if e and obs.ammo > 0 and obs.shoot_cd == 0 then
    if e.dx == 0 then return e.dy > 0 and "shoot_s" or "shoot_n" end
    if e.dy == 0 then return e.dx > 0 and "shoot_e" or "shoot_w" end
  end
  for _ = 1, 4 do
    local i = rand(4)
    if cell(obs, step[i][1], step[i][2]) ~= 1 then return moves[i] end
  end
  return "stay"
end
