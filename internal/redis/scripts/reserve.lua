-- reserve.lua
-- Atomically check available > 0, DECR available, INCR reserved
-- KEYS[1] = sale:{id}:available
-- KEYS[2] = sale:{id}:reserved
-- Returns 1 on success, -1 if sold out

local available = tonumber(redis.call('GET', KEYS[1]))

if not available or available <= 0 then
    return -1
end

redis.call('DECR', KEYS[1])
redis.call('INCR', KEYS[2])

return 1
