-- 模型目录定向可见性（对齐 Agent visible_scope 模式）。
-- visible_scope：all（默认，所有登录用户可见）/ groups（按分组，可细到组内角色）/ roles（按全局角色）。
-- target_audience_json：scope=groups 时存 {group_ids,group_roles}；scope=roles 时存 {role_codes}；scope=all 为 NULL。
ALTER TABLE token_models
  ADD COLUMN visible_scope VARCHAR(32) NOT NULL DEFAULT 'all' AFTER status,
  ADD COLUMN target_audience_json JSON NULL AFTER visible_scope;
