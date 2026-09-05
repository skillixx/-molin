local function decode(raw)
  if type(raw) ~= 'string' then return nil end
  local ok, value = pcall(cjson.decode, raw)
  if not ok or type(value) ~= 'table' then return nil end
  return value
end
local function fields(value,names)
  if type(value)~='table' then return false end
  local allowed,size={},0
  for _,name in ipairs(names) do allowed[name]=true end
  for name,_ in pairs(value) do if not allowed[name] then return false end;size=size+1 end
  return size==#names
end
local function uint_string(value)
  return type(value)=='string' and string.match(value,'^[1-9]%d*$')~=nil and (#value<20 or (#value==20 and value<='18446744073709551615'))
end
local function public_id(value)
  return type(value)=='string' and #value<=128 and string.match(value,'^[A-Za-z0-9][A-Za-z0-9_.%-]*$')~=nil
end
local function identity(raw)
  if type(raw)~='string' or #raw>2048 then return nil end
  local v=decode(raw)
  if not fields(v,{'task','request','user','project','key','model','provider','operation'}) then return nil end
  if not public_id(v.task) or not public_id(v.request) or not uint_string(v.user) or not uint_string(v.project) then return nil end
  if type(v.key)~='string' then return nil end
  local sk=string.match(v.key,'^sk:([1-9]%d*)$')
  if not(sk and uint_string(sk)) and v.key~='jwt:'..v.user..':'..v.project then return nil end
  if type(v.model)~='string' or #v.model>191 or not string.match(v.model,'^[A-Za-z0-9][A-Za-z0-9._/%-]*$') then return nil end
  if v.provider~='fake-native-async' or (v.operation~='text_to_video' and v.operation~='image_to_video') then return nil end
  return v
end
local function epoch_less(a,b)
  return #a<#b or (#a==#b and a<b)
end
local function candidate(raw,epoch,policy)
  if type(raw)~='string' or #raw>131072 then return nil end
  local v=decode(raw)
  if not fields(v,{'schema','epoch','policy','count','records'}) or v.schema~=1 or v.epoch~=epoch or v.policy~=policy or type(v.count)~='number' or v.count%1~=0 or v.count<0 or v.count>102 or type(v.records)~='table' then return nil end
  local count,requests,hard=0,{},{}
  local ceilings={2,10,2,100,100,1,2,1,2,2}
  for task,row in pairs(v.records) do
    count=count+1
    if count>102 or not fields(row,{'identity','attempt','phase'}) then return nil end
    local id=identity(row.identity)
    if not id or id.task~=task or requests[id.request] or type(row.attempt)~='string' or #row.attempt~=64 or not string.match(row.attempt,'^[0-9a-f]+$') or (row.phase~='queued' and row.phase~='running') then return nil end
    requests[id.request]=true
    local scopes={id.user,id.project,id.key,id.model,'global',id.user,id.project,id.key,id.model,id.provider}
    for i=1,10 do
      if (i<=5 and row.phase~='running') or (i>5 and row.phase~='queued') then
        local bucket=tostring(i)..':'..scopes[i];hard[bucket]=(hard[bucket] or 0)+1
        if hard[bucket]>ceilings[i] then return nil end
      end
    end
  end
  if count~=v.count then return nil end
  return v
end
local function current(raw,now)
  if type(raw)~='string' or #raw>131072 then return nil end
  local v=decode(raw)
  if not fields(v,{'schema','epoch','policy','run_id','status','count','records'}) or v.schema~=1 or not uint_string(v.epoch) or type(v.policy)~='string' or not string.match(v.policy,'^[0-9a-f]+$') or #v.policy~=64 or type(v.run_id)~='string' or #v.run_id~=40 or not string.match(v.run_id,'^[0-9a-f]+$') or (v.status~='rebuilding' and v.status~='ready') or type(v.count)~='number' or v.count%1~=0 or v.count<0 or v.count>102 or type(v.records)~='table' then return nil end
  local count,requests,hard=0,{},{}
  local ceilings={2,10,2,100,100,1,2,1,2,2}
  for task,row in pairs(v.records) do
    count=count+1
    if count>102 or not fields(row,{'identity','attempt','phase','expires_ms'}) then return nil end
    local id=identity(row.identity)
    if not id or id.task~=task or requests[id.request] or type(row.attempt)~='string' or #row.attempt~=64 or not string.match(row.attempt,'^[0-9a-f]+$') or (row.phase~='queued' and row.phase~='promoting' and row.phase~='running') or type(row.expires_ms)~='number' or row.expires_ms%1~=0 or row.expires_ms<=0 or row.expires_ms>now+30000 then return nil end
    requests[id.request]=true
    local scopes={id.user,id.project,id.key,id.model,'global',id.user,id.project,id.key,id.model,id.provider}
    for i=1,10 do
      if (i<=5 and row.phase~='running') or (i>5 and row.phase~='queued') then
        local bucket=tostring(i)..':'..scopes[i];hard[bucket]=(hard[bucket] or 0)+1
        if hard[bucket]>ceilings[i] then return nil end
      end
    end
  end
  if count~=v.count then return nil end
  return v
end
local function matches(state,snapshot)
  if state.count~=snapshot.count then return false end
  for task,row in pairs(snapshot.records) do
    local old=state.records[task]
    if not old or old.identity~=row.identity or old.attempt~=row.attempt or old.phase~=row.phase then return false end
  end
  return true
end

local action,epoch,policy=ARGV[1],ARGV[2],ARGV[3]
if (action~='stage' and action~='activate' and action~='inspect' and action~='header' and action~='metrics') or not uint_string(epoch) or type(policy)~='string' or #policy~=64 or not string.match(policy,'^[0-9a-f]+$') then return {0} end
local snapshot=nil
if action~='header' and action~='metrics' then snapshot=candidate(ARGV[4],epoch,policy);if not snapshot then return {0} end end
local actual_run_id=string.match(redis.call('INFO','server'),'run_id:([0-9a-f]+)')
if not actual_run_id or #actual_run_id~=40 then return {0} end
local kind=redis.call('TYPE',KEYS[1]).ok
if kind~='none' and kind~='string' then return {0} end
if kind=='string' and redis.call('PTTL',KEYS[1])~=-1 then return {0} end
local raw=kind=='string' and redis.call('GET',KEYS[1]) or nil
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local state=raw and current(raw,now) or nil
if raw and not state then return {0} end
if action=='header' or action=='metrics' then
  if type(ARGV[4])~='string' or ARGV[4]~=actual_run_id or not state or state.run_id~=actual_run_id or state.epoch~=epoch or state.policy~=policy or state.status~='ready' then return {2} end
  if action=='metrics' then
    local queued,promoting,running=0,0,0
    for _,row in pairs(state.records) do
      if row.phase=='queued' then queued=queued+1 elseif row.phase=='promoting' then promoting=promoting+1 elseif row.phase=='running' then running=running+1 else return {0} end
    end
    return {1,queued,promoting,running}
  end
  return {1,'ready',state.count}
end
if action=='activate' or action=='inspect' then
  if not state or state.run_id~=actual_run_id or state.epoch~=epoch or state.policy~=policy or not matches(state,snapshot) then return {2} end
  if action=='inspect' then return {1,state.status,state.count} end
  if state.status=='ready' then return {1,'ready',state.count} end
  state.status='ready'
  local encoded=cjson.encode(state);if #encoded>131072 then return {0} end
  redis.call('SET',KEYS[1],encoded);return {1,'ready',state.count}
end
if state then
  if state.epoch==epoch then
    if state.run_id~=actual_run_id then return {0} end
    if state.policy~=policy or not matches(state,snapshot) then return {2} end
    return {1,state.status,state.count}
  end
  if not epoch_less(state.epoch,epoch) then return {2} end
end
local records={}
for task,row in pairs(snapshot.records) do records[task]={identity=row.identity,attempt=row.attempt,phase=row.phase,expires_ms=now+30000} end
local staged={schema=1,epoch=epoch,policy=policy,run_id=actual_run_id,status='rebuilding',count=snapshot.count,records=records}
local encoded=cjson.encode(staged);if #encoded>131072 then return {0} end
redis.call('SET',KEYS[1],encoded)
return {1,'rebuilding',snapshot.count}
