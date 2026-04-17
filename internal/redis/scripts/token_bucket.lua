-- Token bucket rate limiter
-- KEYS[1] = ratelimit:{user_id}
-- ARGV[1] = capacity (10)
-- ARGV[2] = rate (2 tokens/sec)
-- ARGV[3] = now (unix ms)

local bucket = redis.call('HMGET', KEYS[1], 'tokens', 'last_refill')
local tokens = tonumber(bucket[1])
local last   = tonumber(bucket[2])
local capacity = tonumber(ARGV[1])
local rate     = tonumber(ARGV[2])
local now      = tonumber(ARGV[3])

if tokens == nil then
    tokens = capacity
    last   = now
end

local elapsed = (now - last) / 1000.0
tokens = math.min(capacity, tokens + elapsed * rate)

if tokens < 1 then
    return -1  -- rate limited
end

tokens = tokens - 1
redis.call('HMSET', KEYS[1], 'tokens', tokens, 'last_refill', now)
redis.call('EXPIRE', KEYS[1], 60)

return math.floor(tokens)  -- remaining tokens
