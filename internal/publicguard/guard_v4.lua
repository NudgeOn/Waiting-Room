#!lua name=wr_public_guard_v4
-- SPDX-License-Identifier: Apache-2.0
-- Admission/queue keys are never read or written here. All memory is bounded.
local function hex64(s) return #s==64 and not string.find(s,'[^a-f0-9]') end
redis.register_function('wr_pg4_check',function(keys,args)
  if #keys~=3 or #args~=6 or #args[1]~=20 or string.find(args[1],'[^a-z2-7]') or not hex64(args[3]) or not hex64(args[5]) then return redis.error_reply('WR_GUARD_SCHEMA') end
  local epoch,cap=tonumber(args[2]),tonumber(args[6])
  if not epoch or epoch<1 or epoch~=math.floor(epoch) or (cap~=10000 and cap~=100000) then return redis.error_reply('WR_GUARD_SCHEMA') end
  local op=args[4]
  if op~='join' and op~='status' and op~='claim' and op~='heartbeat' and op~='register' then return redis.error_reply('WR_GUARD_SCHEMA') end
  local schema=redis.call('HGET',keys[3],'schema')
  if schema and schema~='1' then return redis.error_reply('WR_GUARD_SCHEMA') end
  local time=redis.call('TIME');local now=tonumber(time[1])*1000+math.floor(tonumber(time[2])/1000)
  local old=tonumber(redis.call('HGET',keys[3],'clock')) or 0
  -- Defer briefly without touching counters or reaching the queue until the
  -- real clock catches up. This never grants quota or advances a poll deadline.
  if now<old then
    if old-now>1000 then return redis.error_reply('WR_GUARD_CLOCK') end
    return cjson.encode({allowed=false,now=old,retryAfterMs=old-now,pollAfterMs=3000+tonumber(string.sub(args[5],1,8),16)%17001})
  end
  redis.call('HSET',keys[3],'schema','1','clock',string.format('%.0f',now))
  local expired=redis.call('ZRANGEBYSCORE',keys[2],'-inf',now,'LIMIT',0,128)
  for _,id in ipairs(expired) do redis.call('HDEL',keys[1],id);redis.call('ZREM',keys[2],id) end
  local function result(allow,delay) return cjson.encode({allowed=allow,now=now,retryAfterMs=delay,pollAfterMs=3000+tonumber(string.sub(args[5],1,8),16)%17001}) end
  if redis.call('HLEN',keys[1])~=redis.call('ZCARD',keys[2]) then return redis.error_reply('WR_GUARD_SCHEMA') end
  local room=args[1]..':'..args[2]..':'
  local interval=3000+tonumber(string.sub(args[5],1,8),16)%17001
  local poll='p:'..room..args[5]
  local function put(id,value,until_ms)
    if redis.call('HEXISTS',keys[1],id)==0 and redis.call('HLEN',keys[1])>=cap*4+2048 then return false end
    redis.call('HSET',keys[1],id,value);redis.call('ZADD',keys[2],until_ms,id);return true
  end
  if op=='register' then
    local next=tonumber(redis.call('HGET',keys[1],poll))
    if not next and not put(poll,now+interval,now+60000) then return result(false,3000) end
    return result(true,math.max(0,(next or now+interval)-now))
  end
  -- Fixed minute windows govern abuse, separate from strict rolling admission rate.
  local minute=math.floor(now/60000);local reset=(minute+1)*60000
  local seen='j:'..room..args[5]
  local replay=op=='join' and redis.call('HEXISTS',keys[1],seen)==1
  local fresh=op=='join' and not replay
  local family=fresh and 'join' or 'ticket'
  local source='s:'..room..family..':'..args[3]..':'..minute
  local aggregate='r:'..room..family..':'..minute
  local sourceMax=fresh and 600 or 6000
  local roomMax=fresh and cap*6 or cap*20
  local sc=tonumber(redis.call('HGET',keys[1],source)) or 0
  local rc=tonumber(redis.call('HGET',keys[1],aggregate)) or 0
  if sc>=sourceMax or rc>=roomMax then return result(false,reset-now) end
  -- Reserve only missing fields; full metadata must not reject an existing
  -- ticket whose source, aggregate and schedule already fit in the budget.
  local needed=0
  for _,id in ipairs({source,aggregate}) do if redis.call('HEXISTS',keys[1],id)==0 then needed=needed+1 end end
  if fresh then needed=needed+1 end
  if op=='status' and redis.call('HEXISTS',keys[1],poll)==0 then needed=needed+1 end
  if redis.call('HLEN',keys[1])+needed>cap*4+2048 then return result(false,3000) end
  if not put(source,sc+1,reset) or not put(aggregate,rc+1,reset) then return result(false,3000) end
  if fresh and not put(seen,'1',now+600000) then return result(false,3000) end
  if op=='status' then
    local next=tonumber(redis.call('HGET',keys[1],poll))
    if next and now<next then return result(false,next-now) end
    if not put(poll,now+interval,now+60000) then return result(false,3000) end
    -- An unknown schedule after restart/reconnect is spread across 3..20 seconds.
    if not next then return result(false,interval) end
  end
  return result(true,0)
end)
