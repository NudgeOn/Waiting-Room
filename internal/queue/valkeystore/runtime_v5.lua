#!lua name=wr_queue_runtime_v5
-- SPDX-License-Identifier: Apache-2.0
-- Versioned runtime store, unsharded only; never replaces v1/v2 libraries. All keys are explicit FCALL keys.
local function member(keys,id) return redis.call('HGET',keys[1],'room')..':'..id end
local function epoch_current(keys)
  return #keys==12 and redis.call('HGET',keys[12],'epoch')==redis.call('HGET',keys[9],'epoch')
end
local function available(keys)
  return epoch_current(keys) and redis.call('HGET',keys[9],'schema') == '5' and redis.call('HGET',keys[9],'dirty') == '0' and redis.call('HGET',keys[9],'mode') == 'ACTIVE'
    and tonumber(redis.call('HGET',keys[9],'visitors')) == redis.call('ZCARD',keys[10])
    and tonumber(redis.call('HGET',keys[9],'idems')) == redis.call('ZCARD',keys[11])
end
local function commit(keys)
  local clock=tonumber(redis.call('HGET',keys[9],'clock')) or 0
  redis.call('HSET',keys[12],'clock',string.format('%.0f',math.max(clock,tonumber(redis.call('HGET',keys[12],'clock')) or 0)))
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
    local raw=redis.call('HGET',keys[2],id)
    if not raw then error('missing retained ticket') end
    if cjson.decode(raw).State=='READY' then redis.call('HINCRBY',keys[1],'ready',-1) end
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
-- Fences are checked inside Valkey, including its actual process identity. An
-- INFO response obtained by a client before failover is not an adequate fence.
local function primary_id()
  return string.match(redis.call('INFO','server'),'run_id:([a-f0-9]+)')
end
local function frame(keys,now)
  return cjson.encode({now=now,primary=redis.call('HGET',keys[9],'primary'),fence=tonumber(redis.call('HGET',keys[9],'fence')),mode=redis.call('HGET',keys[9],'mode'),unsafeUntil=tonumber(redis.call('HGET',keys[9],'unsafeUntil')) or 0,reason=redis.call('HGET',keys[9],'recoveryReason') or '',validationError=redis.call('HGET',keys[9],'validationError') or ''})
end
local function fenced(keys,args)
  if not epoch_current(keys) or #args<2 then return false end
  local fence=table.remove(args)
  local primary=table.remove(args)
  return primary==primary_id() and primary==redis.call('HGET',keys[9],'primary') and fence==redis.call('HGET',keys[9],'fence')
end
local function hold(keys,now,primary,reason)
  local fence=tonumber(redis.call('HGET',keys[9],'fence'))
  local lifetime=tonumber(redis.call('HGET',keys[9],'leaseLifetime'))
  local clock=tonumber(redis.call('HGET',keys[9],'clock'))
  if not fence or fence>=9007199254740990 or not lifetime or not clock then return false end
  local until_ms=math.max(now,clock)+math.max(lifetime,60000)+30000
  until_ms=math.max(until_ms,tonumber(redis.call('HGET',keys[9],'unsafeUntil')) or 0)
  redis.call('HSET',keys[9],'mode','RECOVERY_HOLD','primary',primary,'fence',string.format('%.0f',fence+1),'unsafeUntil',string.format('%.0f',until_ms),'handshakeAt',string.format('%.0f',now),'recoveryReason',reason,'validationError','')
  return true
end
redis.register_function('wr_r5_init',function(keys,args)
  if #keys~=12 or #args~=6 or args[2]~=primary_id() then return redis.error_reply('WR_SCHEMA') end
  local epoch,not_before=tonumber(args[5]),tonumber(args[6])
  if not epoch or epoch<1 or epoch>=9007199254740990 or epoch~=math.floor(epoch) or not not_before or not_before<0 or not_before>=9007199254740990 or not_before~=math.floor(not_before) then return redis.error_reply('WR_SCHEMA') end
  local current=tonumber(redis.call('HGET',keys[12],'epoch'))
  if (not current and epoch~=1) or (current and (epoch<current)) then return redis.error_reply('WR_FENCED') end
  local global=redis.call('HGET',keys[9],'config')
  local room=redis.call('HGET',keys[1],'config')
  local registered=redis.call('HGET',keys[9],'room:'..args[4])
  if not room and (registered or redis.call('EXISTS',keys[2],keys[3],keys[4],keys[5],keys[6],keys[7],keys[8])>0) then return redis.error_reply('WR_UNAVAILABLE') end
  if not global and (room or redis.call('EXISTS',keys[9],keys[10],keys[11])>0) then return redis.error_reply('WR_UNAVAILABLE') end
  if global and (global~=args[3] or redis.call('HGET',keys[9],'schema')~='5' or redis.call('HGET',keys[9],'epoch')~=args[5]) then return redis.error_reply('WR_SCHEMA') end
  if room and (redis.call('HGET',keys[1],'room')~=args[4] or redis.call('HGET',keys[1],'schema')~='5' or redis.call('HGET',keys[1],'epoch')~=args[5]) then return redis.error_reply('WR_SCHEMA') end
  local c=cjson.decode(args[1])
  if not current then redis.call('HSET',keys[12],'epoch',args[5],'clock',redis.call('HGET',keys[9],'clock') or '0') end
  if current and epoch>current then
    -- One installation-wide monotonic epoch fences every stale v5 writer.
    -- Keep the maximum supported old admission lifetime, plus clock margin.
    local clock=tonumber(redis.call('HGET',keys[12],'clock'))
    if not clock or clock<0 or clock>=9007199251110990 then return redis.error_reply('WR_SCHEMA') end
    not_before=math.max(not_before,math.max(now_ms(),clock)+3630000)
    redis.call('HSET',keys[12],'epoch',args[5],'unsafeUntil',string.format('%.0f',not_before))
  end
  not_before=math.max(not_before,tonumber(redis.call('HGET',keys[12],'unsafeUntil')) or 0)
  if room then return frame(keys,now_ms()) end
  local old_dirty=redis.call('HGET',keys[9],'dirty')
  if global and (redis.call('HGET',keys[9],'primary')~=args[2] or not available(keys)) then
    if redis.call('HGET',keys[9],'mode')~='RECOVERY_HOLD' or redis.call('HGET',keys[9],'primary')~=args[2] then
      if not hold(keys,now_ms(),args[2],'initialization_uncertainty') then return redis.error_reply('WR_UNAVAILABLE') end
    end
  end
  redis.call('HSET',keys[9],'dirty','1')
  if not global then redis.call('HSET',keys[9],'config',args[3],'schema','5','primary',args[2],'mode','ACTIVE','clock','0','fence','1','unsafeUntil','0','leaseLifetime','60000','epoch',args[5])
    if not_before>now_ms() then redis.call('HSET',keys[9],'mode','RECOVERY_HOLD','unsafeUntil',string.format('%.0f',not_before),'handshakeAt',string.format('%.0f',now_ms()),'recoveryReason','epoch_reset','validationError','') end
  end
  redis.call('HSET',keys[9],'leaseLifetime',string.format('%.0f',math.max(tonumber(redis.call('HGET',keys[9],'leaseLifetime')),c.AdmissionTTL,c.ReadyTTL)))
  redis.call('HSET',keys[1],'config',args[1],'schema','5','epoch',args[5],'room',args[4],'seq','0','mode','HOLD','primary',args[2],'clock','0','configRevision','0','runtimeRevision','0','desired','','ready','0')
  redis.call('HSET',keys[9],'room:'..args[4],'1')
  if old_dirty~='1' then commit(keys) end
  return frame(keys,now_ms())
end)
redis.register_function('wr_r5_recover',function(keys,args)
  if #keys~=12 or #args~=2 or redis.call('HGET',keys[9],'schema')~='5' then return redis.error_reply('WR_SCHEMA') end
  local primary=primary_id()
  if not epoch_current(keys) or primary~=args[1] then return redis.error_reply('WR_FENCED') end
  local now=now_ms()
  local mode=redis.call('HGET',keys[9],'mode')
  local previous=redis.call('HGET',keys[9],'primary')
  local clock=tonumber(redis.call('HGET',keys[9],'clock'))
  if not clock then return redis.error_reply('WR_UNAVAILABLE') end
  local reason=nil
  if previous~=primary then reason='primary_changed'
  elseif now<clock and (mode~='RECOVERY_HOLD' or now<tonumber(redis.call('HGET',keys[9],'handshakeAt'))) then reason='clock_rollback'
  elseif mode~='RECOVERY_HOLD' and args[2]=='uncertain' then reason='uncertain_write'
  elseif mode~='RECOVERY_HOLD' and not available(keys) then reason='state_uncertain' end
  if reason and not hold(keys,now,primary,reason) then return redis.error_reply('WR_UNAVAILABLE') end
  return frame(keys,now)
end)
redis.register_function{function_name='wr_r5_status', flags={'no-writes'}, callback=function(keys,args)
  if not fenced(keys,args) then return redis.error_reply('WR_FENCED') end
  if not available(keys) then return redis.error_reply('WR_UNAVAILABLE') end
  local c=get_config(keys)
  if not c or redis.call('HGET',keys[1],'schema') ~= '5' then return redis.error_reply('WR_UNAVAILABLE') end
  local now=now_ms()
  local raw=redis.call('HGET', keys[2], args[1])
  if not raw then return redis.error_reply('WR_EXPIRED') end
  local t=cjson.decode(raw)
  if expired(t,now,c) then return redis.error_reply('WR_EXPIRED') end
  return answer(now,t)
end}
local function command(keys,args)
  if #keys ~= 12 then return redis.error_reply('WR_SCHEMA') end
  local c=get_config(keys)
  if not c or redis.call('HGET',keys[1],'schema') ~= '5' then return redis.error_reply('WR_UNAVAILABLE') end
  local op=args[1]
  if op ~= 'join' and op ~= 'promote' and op ~= 'claim' and op ~= 'heartbeat' and op ~= 'configure' and op ~= 'sweep' then return redis.error_reply('WR_COMMAND') end
  if op == 'promote' and (not tonumber(args[2]) or tonumber(args[2]) < 1 or tonumber(args[2]) > 128) then return redis.error_reply('WR_COMMAND') end
  if op == 'join' and (#args ~= 5 or #args[2] ~= 64 or #args[3] ~= 64 or #args[4] ~= 64 or #args[5] > 8192) then return redis.error_reply('WR_COMMAND') end
  local now=now_ms()
  if now < tonumber(redis.call('HGET',keys[1],'clock')) then
    hold(keys,now,primary_id(),'clock_rollback')
    return redis.error_reply('WR_UNAVAILABLE')
  end
  redis.call('HSET',keys[1],'clock',tostring(now))
  local mode=redis.call('HGET',keys[1],'mode')
  if mode == 'RECOVERY_HOLD' then return redis.error_reply('WR_UNAVAILABLE') end
  if not cleanup(keys,now) then return redis.error_reply('WR_SWEEP_REQUIRED') end
  if op == 'sweep' then return answer(now) end
  if op == 'configure' then
    if #args~=5 then return redis.error_reply('WR_SCHEMA') end
    local cr,rr=tonumber(args[3]),tonumber(args[4])
    local oldcr,oldrr=tonumber(redis.call('HGET',keys[1],'configRevision')),tonumber(redis.call('HGET',keys[1],'runtimeRevision'))
    if not cr or not rr or cr<oldcr or rr<oldrr then return redis.error_reply('WR_CONFLICT') end
    local desired=args[2]..':'..args[3]..':'..args[4]..':'..args[5]
    if cr==oldcr and rr==oldrr then
      if desired~=redis.call('HGET',keys[1],'desired') then return redis.error_reply('WR_CONFLICT') end
      return answer(now)
    end
    if args[5]~='AUTO' and args[5]~='HOLD' and args[5]~='DRAINING' and args[5]~='OFF' then return redis.error_reply('WR_SCHEMA') end
    local next=cjson.decode(args[2])
    if next.VisitorCap~=c.VisitorCap or next.IdempotencyCap~=c.IdempotencyCap or next.ClockSkew~=c.ClockSkew or next.IdempotencyTTL~=c.IdempotencyTTL then return redis.error_reply('WR_SCHEMA') end
    if (next.IdleTTL~=c.IdleTTL or next.TicketTTL~=c.TicketTTL or next.ReadyTTL~=c.ReadyTTL) and redis.call('HLEN',keys[2])>0 then return redis.error_reply('WR_DRAIN') end
    -- Reducing limits never retroactively violates the currently reported bound.
    -- Wait until already-issued leases/window reservations fit the new limit.
    if next.LeaseCap<redis.call('ZCARD',keys[5]) or next.Rate<redis.call('ZCARD',keys[6]) then return redis.error_reply('WR_DRAIN') end
    redis.call('HSET',keys[9],'leaseLifetime',string.format('%.0f',math.max(tonumber(redis.call('HGET',keys[9],'leaseLifetime')),next.AdmissionTTL,next.ReadyTTL)))
    redis.call('HSET',keys[1],'config',args[2],'configRevision',args[3],'runtimeRevision',args[4],'desired',desired,'mode',args[5])
    if args[5]=='DRAINING' and mode~='DRAINING' then redis.call('HSET',keys[1],'cutoff',redis.call('HGET',keys[1],'seq')) end
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
    if mode == 'DRAINING' or mode=='OFF' then return redis.error_reply('WR_DRAIN') end
    local global=cjson.decode(redis.call('HGET',keys[9],'config'))
    if redis.call('ZCARD',keys[10]) >= global.VisitorCap or redis.call('ZCARD',keys[11]) >= global.IdempotencyCap or redis.call('HLEN',keys[2]) >= c.VisitorCap or redis.call('HLEN',keys[7]) >= c.IdempotencyCap then return redis.error_reply('WR_CAPACITY') end
    if redis.call('HEXISTS',keys[2],args[4]) == 1 then return redis.error_reply('WR_CONFLICT') end
    local seq=tonumber(redis.call('HGET',keys[1],'seq'))
    if seq >= 9007199254740990 then return redis.error_reply('WR_UNAVAILABLE') end
    seq=redis.call('HINCRBY',keys[1],'seq',1)
    local t={ID=args[4],Sequence=seq,State='WAITING',Epoch=tonumber(redis.call('HGET',keys[1],'epoch')),JoinedAt=now,IdleUntil=now+c.IdleTTL,AbsoluteUntil=now+c.TicketTTL,ReadyUntil=0,AdmissionUntil=0,JTI=''}
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
    if mode == 'HOLD' or mode=='OFF' then return cjson.encode(result) end
    local budget=math.min(tonumber(args[2]),c.LeaseCap-redis.call('ZCARD',keys[5]),c.Rate-redis.call('ZCARD',keys[6]))
    if budget <= 0 then return cjson.encode(result) end
    local ids=redis.call('ZRANGE',keys[3],0,budget-1)
    for _,id in ipairs(ids) do
      local t=cjson.decode(redis.call('HGET',keys[2],id))
      if mode == 'DRAINING' and t.Sequence > tonumber(redis.call('HGET',keys[1],'cutoff')) then break end
      t.State='READY';t.PromotedAt=now;t.AdmissionUntil=now+c.AdmissionTTL;t.ReadyUntil=math.min(now+c.ReadyTTL,t.AdmissionUntil)
      t.JTI='admission:'..string.format('%.0f',t.Epoch)..':'..string.format('%.0f',t.Sequence)
      redis.call('HSET',keys[2],id,cjson.encode(t))
      redis.call('HINCRBY',keys[1],'ready',1)
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
      redis.call('HINCRBY',keys[1],'ready',-1)
      t.State='ADMITTED'
      redis.call('HSET',keys[2],t.ID,cjson.encode(t))
      redis.call('ZADD',keys[4],t.AdmissionUntil+c.ClockSkew,t.ID)
      redis.call('ZADD',keys[10],t.AdmissionUntil+c.ClockSkew,member(keys,t.ID))
      redis.call('ZADD',keys[5],t.AdmissionUntil+c.ClockSkew,t.ID)
    end
  end
  return answer(now,t)
end
redis.register_function('wr_r5_command',function(keys,args)
  if not fenced(keys,args) then return redis.error_reply('WR_FENCED') end
  if not available(keys) then return redis.error_reply('WR_UNAVAILABLE') end
  local now=now_ms()
  if now < tonumber(redis.call('HGET',keys[9],'clock')) then
    hold(keys,now,primary_id(),'clock_rollback')
    return redis.error_reply('WR_UNAVAILABLE')
  end
  redis.call('HSET',keys[12],'clock',string.format('%.0f',math.max(now,tonumber(redis.call('HGET',keys[12],'clock')) or 0)))
  redis.call('HSET',keys[9],'dirty','1','clock',tostring(now))
  local result=command(keys,args)
  commit(keys)
  return result
end)
redis.register_function{function_name='wr_r5_capacity',flags={'no-writes'},callback=function(keys,args)
  if not fenced(keys,args) then return redis.error_reply('WR_FENCED') end
  if not available(keys) then return redis.error_reply('WR_UNAVAILABLE') end
  local now=now_ms()
  local c=cjson.decode(redis.call('HGET',keys[9],'config'))
  local visitors=redis.call('ZCOUNT',keys[10],'('..string.format('%.0f',now),'+inf')
  local idems=redis.call('ZCOUNT',keys[11],'('..string.format('%.0f',now),'+inf')
  return cjson.encode({now=now,capacity={visitors=visitors,idempotency=idems,visitorCap=c.VisitorCap,idempotencyCap=c.IdempotencyCap,retainedVisitors=redis.call('ZCARD',keys[10]),retainedIdempotency=redis.call('ZCARD',keys[11]),warning=redis.call('ZCARD',keys[10])*5>=c.VisitorCap*4 or redis.call('ZCARD',keys[11])*5>=c.IdempotencyCap*4}})
end}

-- Validation never reconstructs missing indexes or guesses lost reservations.
-- Admission is held globally throughout validation. Every call visits <=128
-- indexed rows; cardinality checks prove that no unindexed rows were skipped.
local function integer(n)
  return type(n)=='number' and n==n and n>=0 and n<=9007199254740990 and n==math.floor(n)
end
local function hex64(s) return type(s)=='string' and #s==64 and not string.find(s,'[^a-f0-9]') end
local function equal_score(key,id,expected) return tonumber(redis.call('ZSCORE',key,id))==expected end
local function validation_failed(keys,now,reason)
  redis.call('HSET',keys[9],'validationError',reason)
  return frame(keys,now)
end
redis.register_function('wr_r5_validate',function(keys,args)
  if not fenced(keys,args) or #args~=0 then return redis.error_reply('WR_FENCED') end
  local now=now_ms()
  if redis.call('HGET',keys[9],'mode')~='RECOVERY_HOLD' or now<tonumber(redis.call('HGET',keys[9],'unsafeUntil')) or (redis.call('HGET',keys[9],'validationError') or '')~='' then return frame(keys,now) end
  local c=get_config(keys)
  local room=redis.call('HGET',keys[1],'room')
  local fence=redis.call('HGET',keys[9],'fence')
  if not c or not room or redis.call('HGET',keys[1],'schema')~='5' then return validation_failed(keys,now,'room_schema') end
  local tickets=redis.call('HLEN',keys[2])
  local idems=redis.call('HLEN',keys[7])
  if tickets~=redis.call('ZCARD',keys[4]) or idems~=redis.call('ZCARD',keys[8]) or tickets>c.VisitorCap or idems>c.IdempotencyCap then return validation_failed(keys,now,'room_cardinality') end
  if redis.call('HGET',keys[1],'validationFence')~=fence then
    redis.call('HSET',keys[1],'validationFence',fence,'validationPhase','tickets','validationOffset','0','validationWaiting','0','validationReady','0','validationLeases','0','validationMaxSeq','0')
  end
  local phase=redis.call('HGET',keys[1],'validationPhase')
  local offset=tonumber(redis.call('HGET',keys[1],'validationOffset'))
  if phase=='tickets' then
    local ids=redis.call('ZRANGE',keys[4],offset,offset+127,'WITHSCORES')
    local waiting=tonumber(redis.call('HGET',keys[1],'validationWaiting'))
    local ready=tonumber(redis.call('HGET',keys[1],'validationReady'))
    local leases=tonumber(redis.call('HGET',keys[1],'validationLeases'))
    local maxseq=tonumber(redis.call('HGET',keys[1],'validationMaxSeq'))
    for i=1,#ids,2 do
      local id,score=ids[i],tonumber(ids[i+1])
      local ok,t=pcall(cjson.decode,redis.call('HGET',keys[2],id) or '')
      if not ok or type(t)~='table' or not hex64(id) or t.ID~=id or t.Epoch~=tonumber(redis.call('HGET',keys[1],'epoch')) or not integer(t.Sequence) or t.Sequence<1 or not integer(t.JoinedAt) or not integer(t.AbsoluteUntil) or not integer(t.IdleUntil) or t.AbsoluteUntil<t.JoinedAt or t.IdleUntil<t.JoinedAt or t.IdleUntil>t.AbsoluteUntil then return validation_failed(keys,now,'ticket_shape') end
      maxseq=math.max(maxseq,t.Sequence)
      local deadline=nil
      if t.State=='WAITING' then
        waiting=waiting+1;deadline=math.min(t.IdleUntil,t.AbsoluteUntil)
        if not equal_score(keys[3],id,t.Sequence) or redis.call('ZCOUNT',keys[3],t.Sequence,t.Sequence)~=1 or redis.call('ZSCORE',keys[5],id) then return validation_failed(keys,now,'waiting_index') end
      elseif t.State=='READY' or t.State=='ADMITTED' then
        leases=leases+1
        if not integer(t.PromotedAt) or not integer(t.ReadyUntil) or not integer(t.AdmissionUntil) or t.PromotedAt<t.JoinedAt or t.ReadyUntil<=t.PromotedAt or t.ReadyUntil>t.AdmissionUntil or t.AdmissionUntil>now or t.JTI~='admission:'..string.format('%.0f',t.Epoch)..':'..string.format('%.0f',t.Sequence) or redis.call('ZSCORE',keys[3],id) then return validation_failed(keys,now,'admission_state') end
        if t.State=='READY' then ready=ready+1;deadline=t.ReadyUntil else deadline=t.AdmissionUntil+c.ClockSkew end
        if not equal_score(keys[5],id,deadline) then return validation_failed(keys,now,'lease_index') end
      else return validation_failed(keys,now,'ticket_state') end
      if score~=deadline or not equal_score(keys[10],member(keys,id),deadline) then return validation_failed(keys,now,'expiry_owner_index') end
    end
    offset=offset+#ids/2
    redis.call('HSET',keys[1],'validationOffset',offset,'validationWaiting',waiting,'validationReady',ready,'validationLeases',leases,'validationMaxSeq',maxseq)
    if offset>=tickets then
      if waiting~=redis.call('ZCARD',keys[3]) or leases~=redis.call('ZCARD',keys[5]) or ready~=tonumber(redis.call('HGET',keys[1],'ready')) or leases>c.LeaseCap or maxseq>tonumber(redis.call('HGET',keys[1],'seq')) then return validation_failed(keys,now,'reservation_cardinality') end
      redis.call('HSET',keys[1],'validationPhase','idempotency','validationOffset','0')
    end
    return frame(keys,now)
  end
  if phase=='idempotency' then
    local ids=redis.call('ZRANGE',keys[8],offset,offset+127,'WITHSCORES')
    for i=1,#ids,2 do
      local id,score=ids[i],tonumber(ids[i+1])
      local ok,item=pcall(cjson.decode,redis.call('HGET',keys[7],id) or '')
      if not ok or type(item)~='table' or not hex64(id) or not hex64(item.id) or not hex64(item.fingerprint) or type(item.replay)~='string' or #item.replay>8192 or not integer(score) or not equal_score(keys[11],member(keys,id),score) then return validation_failed(keys,now,'idempotency_index') end
    end
    offset=offset+#ids/2
    redis.call('HSET',keys[1],'validationOffset',offset)
    if offset>=idems then
      -- The safety window exceeds a rate window. Future rate entries indicate
      -- corruption/clock uncertainty, not a reason to reset the rate counter.
      if redis.call('ZCOUNT',keys[6],'('..string.format('%.0f',now-60000),'+inf')~=0 or redis.call('ZCARD',keys[6])>c.Rate then return validation_failed(keys,now,'rate_window') end
      redis.call('HSET',keys[1],'validationPhase','done')
      redis.call('HSET',keys[9],'validated:'..room,fence,'validatedVisitors:'..room,tickets,'validatedIdems:'..room,idems)
    end
    return frame(keys,now)
  end
  if phase~='done' then return validation_failed(keys,now,'validation_phase') end
  local total_visitors,total_idems,rooms=0,0,0
  local fields=redis.call('HKEYS',keys[9])
  if #fields>1024 then return validation_failed(keys,now,'installation_metadata') end
  for _,field in ipairs(fields) do
    if string.sub(field,1,5)=='room:' then
      rooms=rooms+1
      local id=string.sub(field,6)
      if redis.call('HGET',keys[9],'validated:'..id)~=fence then return frame(keys,now) end
      total_visitors=total_visitors+tonumber(redis.call('HGET',keys[9],'validatedVisitors:'..id))
      total_idems=total_idems+tonumber(redis.call('HGET',keys[9],'validatedIdems:'..id))
    end
  end
  local global=cjson.decode(redis.call('HGET',keys[9],'config'))
  if rooms<1 or rooms>100 or total_visitors~=redis.call('ZCARD',keys[10]) or total_idems~=redis.call('ZCARD',keys[11]) or total_visitors~=tonumber(redis.call('HGET',keys[9],'visitors')) or total_idems~=tonumber(redis.call('HGET',keys[9],'idems')) or total_visitors>global.VisitorCap or total_idems>global.IdempotencyCap then return validation_failed(keys,now,'installation_cardinality') end
  redis.call('HSET',keys[9],'mode','ACTIVE','dirty','0','clock',string.format('%.0f',now),'recoveredAt',string.format('%.0f',now))
  return frame(keys,now)
end)

redis.register_function{function_name='wr_r5_metrics',flags={'no-writes'},callback=function(keys,args)
  if not fenced(keys,args) then return redis.error_reply('WR_FENCED') end
  if redis.call('HGET',keys[9],'schema')~='5' then return redis.error_reply('WR_UNAVAILABLE') end
  local now=now_ms()
  local waiting=redis.call('ZCARD',keys[3])
  local leases=redis.call('ZCARD',keys[5])
  -- Updated atomically with promotion, claim and bounded expiry cleanup. No
  -- full ticket scan on the monitoring path, even at installation capacity.
  local ready=tonumber(redis.call('HGET',keys[1],'ready'))
  if not ready or ready<0 or ready>leases then return redis.error_reply('WR_UNAVAILABLE') end
  return cjson.encode({now=now,metrics={waiting=waiting,ready=ready,leases=leases,rate=redis.call('ZCOUNT',keys[6],'('..string.format('%.0f',now-60000),'+inf'),mode=redis.call('HGET',keys[9],'mode')=='ACTIVE' and redis.call('HGET',keys[1],'mode') or 'RECOVERY_HOLD',revision=tonumber(redis.call('HGET',keys[1],'runtimeRevision')),epoch=tonumber(redis.call('HGET',keys[1],'epoch')),recoveryUntil=redis.call('HGET',keys[9],'mode')=='ACTIVE' and 0 or tonumber(redis.call('HGET',keys[9],'unsafeUntil'))}})
end}

-- Recording the initial HOLD/OFF of an empty epoch does not admit or mutate
-- retained rows. It lets both nodes ACK while the global safety hold remains.
redis.register_function('wr_r5_configure_held',function(keys,args)
  if not fenced(keys,args) or #args~=5 or args[1]~='configure' then return redis.error_reply('WR_FENCED') end
  if redis.call('HGET',keys[9],'mode')~='RECOVERY_HOLD' or redis.call('HGET',keys[9],'recoveryReason')~='epoch_reset' or redis.call('HLEN',keys[2])~=0 or redis.call('HLEN',keys[7])~=0 or (args[5]~='HOLD' and args[5]~='OFF') then return redis.error_reply('WR_UNAVAILABLE') end
  local cr,rr=tonumber(args[3]),tonumber(args[4])
  local oldcr,oldrr=tonumber(redis.call('HGET',keys[1],'configRevision')),tonumber(redis.call('HGET',keys[1],'runtimeRevision'))
  if not cr or not rr or cr<oldcr or rr<oldrr then return redis.error_reply('WR_CONFLICT') end
  local desired=args[2]..':'..args[3]..':'..args[4]..':'..args[5]
  if cr==oldcr and rr==oldrr and desired~=redis.call('HGET',keys[1],'desired') then return redis.error_reply('WR_CONFLICT') end
  local c=cjson.decode(args[2])
  redis.call('HSET',keys[9],'leaseLifetime',string.format('%.0f',math.max(tonumber(redis.call('HGET',keys[9],'leaseLifetime')),c.AdmissionTTL,c.ReadyTTL)))
  redis.call('HSET',keys[1],'config',args[2],'configRevision',args[3],'runtimeRevision',args[4],'desired',desired,'mode',args[5])
  return answer(now_ms())
end)
