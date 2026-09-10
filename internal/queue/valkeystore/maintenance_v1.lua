#!lua name=wr_queue_maintenance_v1
-- SPDX-License-Identifier: Apache-2.0
-- Read-only scheduling probe for schema 5 / runtime ABI v6. Queue writes still
-- use the unchanged, fenced runtime command and recheck every invariant there.
redis.register_function{function_name='wr_qm1_needed',flags={'no-writes'},callback=function(keys,args)
  if #keys~=12 or #args~=2 then return redis.error_reply('WR_SCHEMA') end
  local primary=string.match(redis.call('INFO','server'),'run_id:([a-f0-9]+)')
  if args[1]~=primary or args[1]~=redis.call('HGET',keys[9],'primary') or args[2]~=redis.call('HGET',keys[9],'fence') or redis.call('HGET',keys[12],'epoch')~=redis.call('HGET',keys[9],'epoch') then return redis.error_reply('WR_FENCED') end
  if redis.call('HGET',keys[9],'schema')~='5' or redis.call('HGET',keys[1],'schema')~='5' or redis.call('HGET',keys[9],'mode')~='ACTIVE' or redis.call('HGET',keys[9],'dirty')~='0' or tonumber(redis.call('HGET',keys[9],'visitors'))~=redis.call('ZCARD',keys[10]) or tonumber(redis.call('HGET',keys[9],'idems'))~=redis.call('ZCARD',keys[11]) then return redis.error_reply('WR_UNAVAILABLE') end
  local t=redis.call('TIME')
  local now=tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000)
  local global_clock=tonumber(redis.call('HGET',keys[9],'clock'))
  local room_clock=tonumber(redis.call('HGET',keys[1],'clock'))
  if not global_clock or not room_clock or now<global_clock or now<room_clock then return redis.error_reply('WR_UNAVAILABLE') end
  local mode=redis.call('HGET',keys[1],'mode')
  if mode~='HOLD' and mode~='OFF' and mode~='AUTO' and mode~='DRAINING' then return redis.error_reply('WR_UNAVAILABLE') end
  local raw=redis.call('HGET',keys[1],'config')
  if not raw then return redis.error_reply('WR_UNAVAILABLE') end
  local c=cjson.decode(raw)
  if not tonumber(c.LeaseCap) or not tonumber(c.Rate) then return redis.error_reply('WR_SCHEMA') end
  -- Each lookup is bounded by one sorted-set entry. Expiry cleanup must still
  -- run in HOLD/OFF, and rate reservations must be released at the real time.
  for _,spec in ipairs({{4,now},{8,now},{6,now-60000}}) do
    if #redis.call('ZRANGEBYSCORE',keys[spec[1]],'-inf',spec[2],'LIMIT',0,1)>0 then return cjson.encode({now=now,maintenanceNeeded=true}) end
  end
  local needed=false
  if mode=='AUTO' or mode=='DRAINING' then
    needed=redis.call('ZCARD',keys[3])>0 and redis.call('ZCARD',keys[5])<c.LeaseCap and redis.call('ZCARD',keys[6])<c.Rate
  end
  return cjson.encode({now=now,maintenanceNeeded=needed})
end}
