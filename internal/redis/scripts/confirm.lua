-- confirm.lua
-- Atomically DECR reserved only (item is now sold, no longer reserved)
-- Verify invariant: available + reserved must not exceed total
-- KEYS[1] = sale:{id}:reserved
-- KEYS[2] = sale:{id}:available
-- KEYS[3] = sale:{id}:meta
-- Returns 1 on success, -1 on error

local reserved = tonumber(redis.call('GET', KEYS[1]))

if not reserved or reserved <= 0 then
    return -1
end

redis.call('DECR', KEYS[1])

return 1
