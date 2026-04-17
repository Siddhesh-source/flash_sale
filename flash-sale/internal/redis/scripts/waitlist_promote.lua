-- waitlist_promote.lua
-- KEYS[1] = sale:{id}:waitlist
-- KEYS[2] = sale:{id}:available
-- KEYS[3] = sale:{id}:reserved
-- Atomically: ZPOPMIN waitlist (get next user), DECR available, INCR reserved
-- Return promoted user_id, or -1 if waitlist is empty or available == 0

local available = tonumber(redis.call('GET', KEYS[2]))

if not available or available <= 0 then
    return '-1'
end

local result = redis.call('ZPOPMIN', KEYS[1], 1)

if not result or #result == 0 then
    return '-1'
end

local user_id = result[1]

redis.call('DECR', KEYS[2])
redis.call('INCR', KEYS[3])

return user_id
