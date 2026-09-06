#!lua name=wr_queue_install_v2
-- SPDX-License-Identifier: Apache-2.0
-- Installation-aware lab store, unsharded only. All keys are explicit FCALL keys.
local function member(keys,id) return redis.call('HGET',keys[1],'room')..':'..id end
local function available(keys)
  return #keys == 11 and redis.call('HGET',keys[9],'schema') == '2' and redis.call('HGET',keys[9],'dirty') == '0' and redis.call('HGET',keys[9],'mode') == 'ACTIVE'
    and tonumber(redis.call('HGET',keys[9],'visitors')) == redis.call('ZCARD',keys[10])
    and tonumber(redis.call('HGET',keys[9],'idems')) == redis.call('ZCARD',keys[11])
end
local function commit(keys)
  redis.call('HSET',keys[9],'visitors',redis.call('ZCARD',keys[10]),'idems',redis.call('ZCARD',keys[11]),'dirty','0')
end
local function now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end
local function answer(now, ticket, replay)
  return cjson.encode({now=now, ticket=ticket or cjson.null, replay=replay or ''})
end
local function get_config(keys)
  local raw = redis.call('HGET', keys[1], 'config')
  if not raw then return nil end
  return cjson.decode(raw)
end
local function expired(t, now, c)
  if t.State == 'WAITING' then return now >= math.min(t.IdleUntil, t.AbsoluteUntil) end
  if t.State == 'READY' then return now >= t.ReadyUntil end
  return now >= t.AdmissionUntil + c.ClockSkew
end
local function cleanup(keys, now)
  local ids = redis.call('ZRANGEBYSCORE', keys[4], '-inf', now, 'LIMIT', 0, 128)
  for _, id in ipairs(ids) do
    redis.call('HDEL', keys[2], id)
    redis.call('ZREM', keys[3], id)
    redis.call('ZREM', keys[4], id)
    redis.call('ZREM', keys[5], id)
    redis.call('ZREM',keys[10],member(keys,id))
  end
  local idems = redis.call('ZRANGEBYSCORE', keys[8], '-inf', now, 'LIMIT', 0, 128)
  for _, id in ipairs(idems) do
    redis.call('HDEL', keys[7], id)
    redis.call('ZREM', keys[8], id)
    redis.call('ZREM',keys[11],member(keys,id))
  end
  redis.call('ZREMRANGEBYSCORE', keys[6], '-inf', now - 60000)
  -- Conservatively hold writes until bounded sweeps have caught up.
  -- Global reservations are released only with the owning Room's physical records.
  return redis.call('ZCOUNT', keys[4], '-inf', now) == 0 and redis.call('ZCOUNT', keys[8], '-inf', now) == 0
end
redis.register_function('wr_i2_init',function(keys,args)
  if #keys~=11 or #args~=4 then return redis.error_reply('WR_SCHEMA') end
  local global=redis.call('HGET',keys[9],'config')
  local room=redis.call('HGET',keys[1],'config')
  local registered=redis.call('HGET',keys[9],'room:'..args[4])
  if not room and (registered or redis.call('EXISTS',keys[2],keys[3],keys[4],keys[5],keys[6],keys[7],keys[8])>0) then return redis.error_reply('WR_UNAVAILABLE') end
  if not global and (room or redis.call('EXISTS',keys[9],keys[10],keys[11]) > 0) then return redis.error_reply('WR_UNAVAILABLE') end
  if global and (global~=args[3] or redis.call('HGET',keys[9],'schema')~='2') then return redis.error_reply('WR_SCHEMA') end
  if room and (room~=args[1] or redis.call('HGET',keys[1],'room')~=args[4] or redis.call('HGET',keys[1],'schema')~='2') then return redis.error_reply('WR_SCHEMA') end
  if global and (not available(keys) or redis.call('HGET',keys[9],'primary')~=args[2]) then return redis.error_reply('WR_UNAVAILABLE') end
  -- Marker is set before multi-key writes. Unexpected script errors leave it set.
  redis.call('HSET',keys[9],'dirty','1')
  if not global then redis.call('HSET',keys[9],'config',args[3],'schema','2','primary',args[2],'mode','ACTIVE','clock','0') end
  if not room then redis.call('HSET',keys[1],'config',args[1],'schema','2','room',args[4],'seq','0','mode','AUTO','primary',args[2],'clock','0') end
  redis.call('HSET',keys[9],'room:'..args[4],'1')
  commit(keys)
  return 'OK'
end)
redis.register_function{function_name='wr_i2_status', flags={'no-writes'}, callback=function(keys,args)
  if not available(keys) then return redis.error_reply('WR_UNAVAILABLE') end
  local c=get_config(keys)
  if not c or redis.call('HGET',keys[1],'schema') ~= '2' then return redis.error_reply('WR_UNAVAILABLE') end
  local now=now_ms()
  local raw=redis.call('HGET', keys[2], args[1])
  if not raw then return redis.error_reply('WR_EXPIRED') end
  local t=cjson.decode(raw)
  if expired(t,now,c) then return redis.error_reply('WR_EXPIRED') end
  return answer(now,t)
end}
local function command(keys,args)
  if #keys ~= 11 then return redis.error_reply('WR_SCHEMA') end
  local c=get_config(keys)
  if not c or redis.call('HGET',keys[1],'schema') ~= '2' then return redis.error_reply('WR_UNAVAILABLE') end
  local op=args[1]
  if op ~= 'join' and op ~= 'promote' and op ~= 'claim' and op ~= 'heartbeat' and op ~= 'hold' and op ~= 'auto' and op ~= 'drain' and op ~= 'sweep' then return redis.error_reply('WR_COMMAND') end
  if op == 'promote' and (not tonumber(args[2]) or tonumber(args[2]) < 1 or tonumber(args[2]) > 128) then return redis.error_reply('WR_COMMAND') end
  if op == 'join' and (#args ~= 5 or #args[2] ~= 64 or #args[3] ~= 64 or #args[4] ~= 64 or #args[5] > 8192) then return redis.error_reply('WR_COMMAND') end
  local now=now_ms()
  if now < tonumber(redis.call('HGET',keys[1],'clock')) then
    redis.call('HSET',keys[1],'mode','RECOVERY_HOLD')
    return redis.error_reply('WR_UNAVAILABLE')
  end
  redis.call('HSET',keys[1],'clock',tostring(now))
  local mode=redis.call('HGET',keys[1],'mode')
  if mode == 'RECOVERY_HOLD' then return redis.error_reply('WR_UNAVAILABLE') end
  if not cleanup(keys,now) then return redis.error_reply('WR_SWEEP_REQUIRED') end
  if op == 'sweep' then return answer(now) end
  if op == 'hold' or op == 'auto' or op == 'drain' then
    redis.call('HSET',keys[1],'mode',op == 'hold' and 'HOLD' or (op == 'auto' and 'AUTO' or 'DRAINING'))
    if op == 'drain' then redis.call('HSET',keys[1],'cutoff',redis.call('HGET',keys[1],'seq')) end
    return answer(now)
  end
  if op == 'join' then
    local old=redis.call('HGET',keys[7],args[2])
    if old then
      old=cjson.decode(old)
      if old.fingerprint ~= args[3] then return redis.error_reply('WR_CONFLICT') end
      local raw=redis.call('HGET',keys[2],old.id)
      if not raw then return redis.error_reply('WR_EXPIRED') end
      return answer(now,cjson.decode(raw),old.replay)
    end
    if mode == 'DRAINING' then return redis.error_reply('WR_DRAIN') end
    local global=cjson.decode(redis.call('HGET',keys[9],'config'))
    if redis.call('ZCARD',keys[10]) >= global.VisitorCap or redis.call('ZCARD',keys[11]) >= global.IdempotencyCap or redis.call('HLEN',keys[2]) >= c.VisitorCap or redis.call('HLEN',keys[7]) >= c.IdempotencyCap then return redis.error_reply('WR_CAPACITY') end
    if redis.call('HEXISTS',keys[2],args[4]) == 1 then return redis.error_reply('WR_CONFLICT') end
    local seq=tonumber(redis.call('HGET',keys[1],'seq'))
    if seq >= 9007199254740990 then return redis.error_reply('WR_UNAVAILABLE') end
    seq=redis.call('HINCRBY',keys[1],'seq',1)
    local t={ID=args[4],Sequence=seq,State='WAITING',Epoch=1,JoinedAt=now,IdleUntil=now+c.IdleTTL,AbsoluteUntil=now+c.TicketTTL,ReadyUntil=0,AdmissionUntil=0,JTI=''}
    redis.call('ZADD',keys[10],math.min(t.IdleUntil,t.AbsoluteUntil),member(keys,t.ID))
    redis.call('ZADD',keys[11],now+c.IdempotencyTTL,member(keys,args[2]))
    redis.call('HSET',keys[2],t.ID,cjson.encode(t))
    redis.call('ZADD',keys[3],seq,t.ID)
    redis.call('ZADD',keys[4],math.min(t.IdleUntil,t.AbsoluteUntil),t.ID)
    redis.call('HSET',keys[7],args[2],cjson.encode({id=t.ID,fingerprint=args[3],replay=args[5]}))
    redis.call('ZADD',keys[8],now+c.IdempotencyTTL,args[2])
    return answer(now,t,args[5])
  end
  if op == 'promote' then
    local result={now=now,tickets={}}
    if mode == 'HOLD' then return cjson.encode(result) end
    local budget=math.min(tonumber(args[2]),c.LeaseCap-redis.call('ZCARD',keys[5]),c.Rate-redis.call('ZCARD',keys[6]))
    if budget <= 0 then return cjson.encode(result) end
    local ids=redis.call('ZRANGE',keys[3],0,budget-1)
    for _,id in ipairs(ids) do
      local t=cjson.decode(redis.call('HGET',keys[2],id))
      if mode == 'DRAINING' and t.Sequence > tonumber(redis.call('HGET',keys[1],'cutoff')) then break end
      t.State='READY';t.AdmissionUntil=now+c.AdmissionTTL;t.ReadyUntil=math.min(now+c.ReadyTTL,t.AdmissionUntil)
      t.JTI='admission:1:'..string.format('%.0f',t.Sequence)
      redis.call('HSET',keys[2],id,cjson.encode(t))
      redis.call('ZREM',keys[3],id)
      redis.call('ZADD',keys[4],t.ReadyUntil,id)
      redis.call('ZADD',keys[10],t.ReadyUntil,member(keys,id))
      redis.call('ZADD',keys[5],t.ReadyUntil,id)
      redis.call('ZADD',keys[6],now,id)
      table.insert(result.tickets,t)
    end
    return cjson.encode(result)
  end
  local raw=redis.call('HGET',keys[2],args[2])
  if not raw then return redis.error_reply('WR_EXPIRED') end
  local t=cjson.decode(raw)
  if op == 'heartbeat' then
    if t.State == 'WAITING' then
      t.IdleUntil=math.min(now+c.IdleTTL,t.AbsoluteUntil)
      redis.call('HSET',keys[2],t.ID,cjson.encode(t))
      redis.call('ZADD',keys[4],t.IdleUntil,t.ID)
      redis.call('ZADD',keys[10],t.IdleUntil,member(keys,t.ID))
    end
  elseif op == 'claim' then
    if t.State == 'WAITING' then return redis.error_reply('WR_NOT_READY') end
    if now >= t.AdmissionUntil then return redis.error_reply('WR_EXPIRED') end
    if t.State == 'READY' then
      t.State='ADMITTED'
      redis.call('HSET',keys[2],t.ID,cjson.encode(t))
      redis.call('ZADD',keys[4],t.AdmissionUntil+c.ClockSkew,t.ID)
      redis.call('ZADD',keys[10],t.AdmissionUntil+c.ClockSkew,member(keys,t.ID))
      redis.call('ZADD',keys[5],t.AdmissionUntil+c.ClockSkew,t.ID)
    end
  end
  return answer(now,t)
end
redis.register_function('wr_i2_command',function(keys,args)
  if not available(keys) then return redis.error_reply('WR_UNAVAILABLE') end
  local now=now_ms()
  if now < tonumber(redis.call('HGET',keys[9],'clock')) then
    redis.call('HSET',keys[9],'mode','RECOVERY_HOLD')
    return redis.error_reply('WR_UNAVAILABLE')
  end
  redis.call('HSET',keys[9],'dirty','1','clock',tostring(now))
  local result=command(keys,args)
  commit(keys)
  return result
end)
redis.register_function{function_name='wr_i2_capacity',flags={'no-writes'},callback=function(keys,args)
  if not available(keys) then return redis.error_reply('WR_UNAVAILABLE') end
  local now=now_ms()
  local c=cjson.decode(redis.call('HGET',keys[9],'config'))
  local visitors=redis.call('ZCOUNT',keys[10],'('..string.format('%.0f',now),'+inf')
  local idems=redis.call('ZCOUNT',keys[11],'('..string.format('%.0f',now),'+inf')
  return cjson.encode({now=now,capacity={visitors=visitors,idempotency=idems,visitorCap=c.VisitorCap,idempotencyCap=c.IdempotencyCap,retainedVisitors=redis.call('ZCARD',keys[10]),retainedIdempotency=redis.call('ZCARD',keys[11]),warning=redis.call('ZCARD',keys[10])*5>=c.VisitorCap*4 or redis.call('ZCARD',keys[11])*5>=c.IdempotencyCap*4}})
end}
