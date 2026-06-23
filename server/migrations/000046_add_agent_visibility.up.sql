-- Agent 定向可见性（第三阶段·阶段二）：给 agents 加两列。
-- visible_scope 默认 all → 现有 Agent 行为不变（全员可见），向后兼容。
-- target_audience_json 按 visible_scope 解释：
--   groups → {"group_ids":[10,12],"group_roles":["admin"]}（group_roles 可空=组内任意角色）
--   roles  → {"role_codes":["vip","merchant"]}
-- members/users 为预留枚举，本期后端校验拒绝（不在此 seed）。
ALTER TABLE agents
  ADD COLUMN visible_scope VARCHAR(16) NOT NULL DEFAULT 'all'
      COMMENT 'all 全体 / groups 指定分组(可按组内角色) / roles 指定全局角色（预留 members/users）' AFTER status,
  ADD COLUMN target_audience_json JSON NULL
      COMMENT 'groups:{"group_ids":[],"group_roles":[]} / roles:{"role_codes":[]}' AFTER visible_scope;
