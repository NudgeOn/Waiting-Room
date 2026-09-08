#!lua name=wr_queue_migration_v1
-- SPDX-License-Identifier: Apache-2.0
-- Owner-only, explicit stopped-writer migration. Runtime ACLs cannot access the
-- final maintenance key. Ticket/replay records and every index stay untouched.
local function kind(key,want)
  local actual=redis.call('TYPE',key).ok
  return actual=='none' or actual==want
end
local function now_ms() local t=redis.call('TIME');return tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000) end
local function inspect(keys)
  if #keys<4 or (#keys-4)%8~=0 or #keys>804 then return nil end
  local ns=string.match(keys[1],'^(wr:[a-z]+:[a-zA-Z0-9_-]+){installation:1}:meta$')
  if not ns or keys[2]~=ns..'{installation:1}:visitors' or keys[3]~=ns..'{installation:1}:idempotency' or keys[#keys]~='wr:maintenance:'..string.sub(ns,4) or not kind(keys[#keys],'hash') then return nil end
  if redis.call('EXISTS',keys[1])==0 then
    if #keys~=4 or redis.call('EXISTS',keys[2],keys[3])~=0 then return nil end
    return {state='empty',from=0,to=5,rooms={},retainedVisitors=0,retainedIdempotency=0,snapshot='',minimumHoldMillis=0}
  end
  if not kind(keys[1],'hash') or not kind(keys[2],'zset') or not kind(keys[3],'zset') then return nil end
  local schema=redis.call('HGET',keys[1],'schema')
  if schema=='5' then return {state='current',from=5,to=5,rooms={},retainedVisitors=0,retainedIdempotency=0,snapshot='',minimumHoldMillis=0} end
  if (schema~='3' and schema~='4') or redis.call('HGET',keys[1],'dirty')~='0' then return nil end
  local fields=redis.call('HKEYS',keys[1]);if #fields>1024 then return nil end
  local registered={};local count=0
  for _,field in ipairs(fields) do if string.sub(field,1,5)=='room:' then registered[string.sub(field,6)]=true;count=count+1 end end
  if count<1 or count>100 or #keys~=4+count*8 then return nil end
  local rooms={};local snapshot={redis.call('DUMP',keys[1])};local visitors,idems=0,0
  local lifetime=math.max(60000,tonumber(redis.call('HGET',keys[1],'leaseLifetime')) or 0)
  for first=4,#keys-1,8 do
    local room=string.match(keys[first],'{([a-z2-7]+):1}:meta$')
    if not room or #room~=20 or not registered[room] then return nil end;registered[room]=nil
    local suffix={'meta','tickets','waiting','expiry','leases','rate','idempotency','idem-expiry'}
    for j=0,7 do if keys[first+j]~=ns..'{'..room..':1}:'..suffix[j+1] or not kind(keys[first+j],(j==0 or j==1 or j==6) and 'hash' or 'zset') then return nil end end
    if redis.call('HGET',keys[first],'schema')~=schema or redis.call('HGET',keys[first],'room')~=room then return nil end
    local ok,c=pcall(cjson.decode,redis.call('HGET',keys[first],'config') or '')
    if not ok or type(c)~='table' or type(c.AdmissionTTL)~='number' or type(c.ReadyTTL)~='number' or c.AdmissionTTL<1000 or c.AdmissionTTL>3600000 or c.ReadyTTL<1000 or c.ReadyTTL>3600000 then return nil end
    local v,i=redis.call('HLEN',keys[first+1]),redis.call('HLEN',keys[first+6])
    if v>100000 or i>200000 or v~=redis.call('ZCARD',keys[first+3]) or i~=redis.call('ZCARD',keys[first+7]) then return nil end
    visitors=visitors+v;idems=idems+i;lifetime=math.max(lifetime,c.AdmissionTTL,c.ReadyTTL)
    table.insert(rooms,room);table.insert(snapshot,redis.call('DUMP',keys[first]));table.insert(snapshot,tostring(v));table.insert(snapshot,tostring(i))
  end
  if visitors~=redis.call('ZCARD',keys[2]) or idems~=redis.call('ZCARD',keys[3]) or visitors~=tonumber(redis.call('HGET',keys[1],'visitors')) or idems~=tonumber(redis.call('HGET',keys[1],'idems')) or visitors>100000 or idems>200000 then return nil end
  -- DUMP bytes are hashed before JSON encoding; raw metadata may not be UTF-8.
  for i,item in ipairs(snapshot) do snapshot[i]=redis.sha1hex(item) end
  return {state='prepared',from=tonumber(schema),to=5,rooms=rooms,retainedVisitors=visitors,retainedIdempotency=idems,snapshot=cjson.encode(snapshot),minimumHoldMillis=lifetime+30000}
end
redis.register_function{function_name='wr_qm_plan',flags={'no-writes'},callback=function(keys,args)
  if #args~=0 then return redis.error_reply('WR_SCHEMA') end
  local report=inspect(keys);if not report then return redis.error_reply('WR_SCHEMA') end;return cjson.encode(report)
end}
redis.register_function('wr_qm_apply',function(keys,args)
  if #args~=3 or #args[2]~=64 or string.find(args[2],'[^a-f0-9]') then return redis.error_reply('WR_SCHEMA') end
  local current=inspect(keys);if not current then return redis.error_reply('WR_SCHEMA') end
  if current.state=='current' then
    if redis.call('HGET',keys[#keys],'digest')==args[2] then return redis.call('HGET',keys[#keys],'report') end
    return redis.error_reply('WR_CONFLICT')
  end
  if current.state~='prepared' or current.snapshot~=args[1] then return redis.error_reply('WR_CONFLICT') end
  local next_fence=(tonumber(redis.call('HGET',keys[1],'fence')) or 0)+1
  if next_fence<1 or next_fence>=9007199254740990 or next_fence~=math.floor(next_fence) then return redis.error_reply('WR_SCHEMA') end
  local now=now_ms();local clock=tonumber(redis.call('HGET',keys[1],'clock'));local retained=tonumber(args[3]);local primary=string.match(redis.call('INFO','server'),'run_id:([a-f0-9]+)')
  if not clock or clock<0 or clock>=9007199254740990 or not retained or retained<0 or retained>=9007199254740990 or retained~=math.floor(retained) or not primary then return redis.error_reply('WR_SCHEMA') end
  local until_ms=math.max(math.max(now,clock)+current.minimumHoldMillis,retained+30000,tonumber(redis.call('HGET',keys[1],'unsafeUntil')) or 0)
  if until_ms>=9007199254740990 then return redis.error_reply('WR_SCHEMA') end
  -- Every key type/cardinality and argument was checked before the first write.
  redis.call('HSET',keys[1],'dirty','1','mode','RECOVERY_HOLD','schema','5','epoch','1','fence',string.format('%.0f',next_fence),'primary',primary,'clock',string.format('%.0f',math.max(now,clock)),'unsafeUntil',string.format('%.0f',until_ms),'leaseLifetime',string.format('%.0f',current.minimumHoldMillis-30000),'handshakeAt',string.format('%.0f',now),'recoveryReason','schema_migration','validationError','')
  for first=4,#keys-1,8 do
    local room=redis.call('HGET',keys[first],'room')
    redis.call('HSET',keys[first],'schema','5','epoch','1');redis.call('HDEL',keys[first],'validationFence')
    redis.call('HDEL',keys[1],'validated:'..room,'validatedVisitors:'..room,'validatedIdems:'..room)
  end
  -- Keep dirty=1 until the existing bounded runtime validator proves every row.
  current.state='recovery_hold';current.unsafeUntil=until_ms;current.snapshot=nil;current.digest=args[2]
  local report=cjson.encode(current);redis.call('HSET',keys[#keys],'digest',args[2],'report',report)
  return report
end)
