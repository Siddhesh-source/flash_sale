-- release.lua
-- Atomically INCR available, DECR reserved
-- Used on cancellation or TTL expiry
-- KEYS[1] = sale:{id}:available
-- KEYS[2] = sale:{id}:reserved

redis.call('INCR', KEYS[1])
redis.call('DECR', KEYS[2])

return 1
