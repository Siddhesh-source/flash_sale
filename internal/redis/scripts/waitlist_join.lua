-- waitlist_join.lua
-- KEYS[1] = sale:{id}:available
-- KEYS[2] = sale:{id}:waitlist
-- ARGV[1] = user_id
-- ARGV[2] = current unix timestamp (score)
-- If available > 0, return -1 (user should reserve directly, not join waitlist)
-- If user already in waitlist, return -2
-- Otherwise ZADD waitlist score user_id, return rank (0-indexed position)

local available = tonumber(redis.call('GET', KEYS[1]))

if available and available > 0 then
    return -1
end

local existing = redis.call('ZSCORE', KEYS[2], ARGV[1])
if existing then
    return -2
end

redis.call('ZADD', KEYS[2], ARGV[2], ARGV[1])
local rank = redis.call('ZRANK', KEYS[2], ARGV[1])

return rank
