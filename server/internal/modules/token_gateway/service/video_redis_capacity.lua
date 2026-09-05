-- 单一有界快照避免多桶分别增减出现部分事实；空库和损坏状态绝不自动初始化。
local function decode(raw)
  if type(raw) ~= 'string' then return nil end
  local ok, value = pcall(cjson.decode, raw)
  if not ok or type(value) ~= 'table' then return nil end
  return value
end
local function fields(value, names)
  if type(value) ~= 'table' then return false end
  local allowed, size = {}, 0
  for _, name in ipairs(names) do allowed[name] = true end
  for name, _ in pairs(value) do
    if not allowed[name] then return false end
    size = size + 1
  end
  return size == #names
end
local function uint_string(value)
  return type(value) == 'string' and string.match(value, '^[1-9]%d*$') ~= nil
    and (#value < 20 or (#value == 20 and value <= '18446744073709551615'))
end
local function public_id(value)
  return type(value) == 'string' and #value <= 128 and string.match(value, '^[A-Za-z0-9][A-Za-z0-9_.%-]*$') ~= nil
end
local function identity(raw)
  if type(raw) ~= 'string' or #raw > 2048 then return nil end
  local v = decode(raw)
  if not fields(v, {'task','request','user','project','key','model','provider','operation'}) then return nil end
  if not public_id(v.task) or not public_id(v.request) or not uint_string(v.user) or not uint_string(v.project) then return nil end
  if type(v.key) ~= 'string' then return nil end
  local sk = string.match(v.key, '^sk:([1-9]%d*)$')
  if not (sk and uint_string(sk)) and v.key ~= 'jwt:' .. v.user .. ':' .. v.project then return nil end
  if type(v.model) ~= 'string' or #v.model > 191 or not string.match(v.model, '^[A-Za-z0-9][A-Za-z0-9._/%-]*$') then return nil end
  if v.provider ~= 'fake-native-async' or (v.operation ~= 'text_to_video' and v.operation ~= 'image_to_video') then return nil end
  return v
end
local action, epoch, policy, task, raw_identity, nonce = ARGV[1], ARGV[2], ARGV[3], ARGV[4], ARGV[5], ARGV[6]
if action ~= 'reserve' and action ~= 'read' and action ~= 'prepare' and action ~= 'renew' and action ~= 'confirm' and action ~= 'release' and action ~= 'abort' then return {0} end
local target = identity(raw_identity)
if not target or target.task ~= task or type(nonce) ~= 'string' or #nonce ~= 64 or not string.match(nonce, '^[0-9a-f]+$') then return {0} end
local limits = decode(ARGV[7])
local ceilings = {2,10,2,100,100,1,2,1,2,2}
if not limits or #limits ~= 10 then return {0} end
for i = 1, 10 do
  if type(limits[i]) ~= 'number' or limits[i] % 1 ~= 0 or limits[i] < 1 or limits[i] > ceilings[i] then return {0} end
end
local kind = redis.call('TYPE', KEYS[1]).ok
if kind ~= 'string' or redis.call('PTTL', KEYS[1]) ~= -1 then return {0} end
if redis.call('STRLEN', KEYS[1]) > 131072 then return {0} end
local raw = redis.call('GET', KEYS[1])
if not raw or #raw > 131072 then return {0} end
local state = decode(raw)
if not fields(state, {'schema','epoch','policy','run_id','status','count','records'}) then return {0} end
if state.schema ~= 1 or state.epoch ~= epoch or state.policy ~= policy or state.status ~= 'ready'
  or type(state.records) ~= 'table' or type(state.count) ~= 'number' or state.count % 1 ~= 0 or state.count < 0 or state.count > 102 then return {0} end
-- 在实际执行脚本的连接内复验实例身份；重启保留下来的旧ready快照不能授权新写入。
local actual_run_id = string.match(redis.call('INFO', 'server'), 'run_id:([0-9a-f]+)')
if not actual_run_id or #actual_run_id ~= 40 or state.run_id ~= actual_run_id then return {0} end
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local counts, hard_counts = {0,0,0,0,0,0,0,0,0,0}, {}
local count, request_owners = 0, {}
local target_scopes = {target.user,target.project,target.key,target.model,'global',target.user,target.project,target.key,target.model,target.provider}
for id, row in pairs(state.records) do
  count = count + 1
  if count > 102 or not fields(row, {'identity','attempt','phase','expires_ms'}) then return {0} end
  local subject = identity(row.identity)
  if not subject or subject.task ~= id or type(row.attempt) ~= 'string' or #row.attempt ~= 64 or not string.match(row.attempt, '^[0-9a-f]+$') then return {0} end
  if request_owners[subject.request] then return {0} end
  request_owners[subject.request] = id
  if row.phase ~= 'queued' and row.phase ~= 'promoting' and row.phase ~= 'running' then return {0} end
  if type(row.expires_ms) ~= 'number' or row.expires_ms % 1 ~= 0 or row.expires_ms <= 0 or row.expires_ms > now + 30000 then return {0} end
  local scopes = {subject.user,subject.project,subject.key,subject.model,'global',subject.user,subject.project,subject.key,subject.model,subject.provider}
  for i = 1,10 do
    if (i <= 5 and row.phase ~= 'running') or (i > 5 and row.phase ~= 'queued') then
      local bucket = tostring(i) .. ':' .. scopes[i]
      hard_counts[bucket] = (hard_counts[bucket] or 0) + 1
      if hard_counts[bucket] > ceilings[i] then return {0} end
      if scopes[i] == target_scopes[i] then counts[i] = counts[i] + 1 end
    end
  end
end
if count ~= state.count then return {0} end
if request_owners[target.request] and request_owners[target.request] ~= task then return {2} end
local row = state.records[task]
if row then
  if row.identity ~= raw_identity or row.attempt ~= nonce then return {2} end
  -- Read允许观察过期债务；重复申请不续期，过期持有者不得恢复写权限。
  -- Release由上层持久终态证明授权，允许清理过期债务；其余旧持有者不能恢复写权限。
  if action ~= 'read' and action ~= 'release' and row.expires_ms <= now then return {3} end
  if action == 'read' or action == 'reserve' then return {1,row.phase,row.expires_ms,now} end
  if action == 'prepare' then
    if row.phase ~= 'queued' then return {1,row.phase,row.expires_ms,now} end
    local names = {'user','project','api_key','model','provider'}
    for i = 6,10 do if counts[i] >= limits[i] then return {4,names[i-5]} end end
    -- 预留全部running后仍保留queued；没有MySQL提交证明时不减少排队占用。
    row.phase = 'promoting'
  elseif action == 'confirm' then
    if row.phase == 'running' then return {1,row.phase,row.expires_ms,now} end
    if row.phase ~= 'promoting' then return {3} end
    -- MySQL状态已确定提交后才由上层调用；切到running同时移除queued计数并刷新技术租期。
    row.phase = 'running'
    row.expires_ms = now + 30000
  elseif action == 'renew' then
    row.expires_ms = now + 30000
  elseif action == 'abort' then
    if row.phase == 'queued' then return {1,row.phase,row.expires_ms,now} end
    if row.phase ~= 'promoting' then return {3} end
    -- 仅撤销尚未提交的running预留，保留原queued债务和原技术截止，不借机续期。
    row.phase = 'queued'
  elseif action == 'release' then
    state.records[task] = nil
    state.count = count - 1
  end
else
  -- 同一安全终态释放的重放是只读成功；不存在记录不能为其他动作凭空创建许可。
  if action == 'release' then return {1,'released',now,now} end
  if action ~= 'reserve' then return {3} end
  local names = {'user','project','api_key','model','global'}
  for i = 1,5 do if counts[i] >= limits[i] then return {4,names[i]} end end
  if count >= 102 then return {0} end
  row = {identity=raw_identity,attempt=nonce,phase='queued',expires_ms=now+30000}
  state.records[task], state.count = row, count + 1
end
local ok, changed = pcall(cjson.encode, state)
if not ok or #changed > 131072 then return {0} end
-- 所有形状、代次、限额和序列化校验完成后才作唯一写入；不声称Lua错误能回滚先前命令。
redis.call('SET', KEYS[1], changed)
if action == 'release' then return {1,'released',now,now} end
return {1,row.phase,row.expires_ms,now}
