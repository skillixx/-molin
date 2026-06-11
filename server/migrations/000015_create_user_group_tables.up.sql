-- Phase 0：用户分组基础表（纯 DDL，不接任何业务逻辑）
--
-- 背景：引入「用户分组」能力，支撑三层用户管理模型：
--   超级管理员（管全部）/ 组管理员（管本组）/ 普通组员（用本组应用）。
-- 本 migration 只新增 4 张表，不 ALTER 任何现有表（users/roles 等），
-- 也不接入登录/鉴权/注册逻辑，因此对线上现有功能零影响、完全可回滚。
--
-- 后续阶段：
--   Phase 1 组/成员管理 CRUD（含 group:manage 等权限码 seed）
--   Phase 2 组员继承组权限（改 CheckPermission + 缓存批量失效）
--   Phase 3 数据范围（组管理员只管本组用户）
--   Phase 4 邀请码注册（按渠道/邀请码落默认组）

-- 1. 用户分组：组是权限/应用的容器
--    type：region(区域) / org(机构) / custom(自定义)
--    parent_id：预留层级（如 上海 > 浦东），当前阶段不使用
--    is_default：默认组标记，无邀请码注册时的兜底组（单一默认由应用层保证）
CREATE TABLE IF NOT EXISTS user_groups (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(128) NOT NULL,
  name VARCHAR(128) NOT NULL,
  type VARCHAR(32) NOT NULL DEFAULT 'custom',
  parent_id BIGINT UNSIGNED NULL,
  is_default TINYINT(1) NOT NULL DEFAULT 0,
  description VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_groups_code (code),
  KEY idx_user_groups_type (type),
  KEY idx_user_groups_parent (parent_id),
  KEY idx_user_groups_is_default (is_default)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 2. 用户 ↔ 分组成员关系（多对多）
--    group_role：admin(组管理员，可管本组用户) / member(普通组员)
--    一个用户可在多个组、且各组身份不同（A 组当管理员、B 组当组员）
CREATE TABLE IF NOT EXISTS user_group_members (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  group_id BIGINT UNSIGNED NOT NULL,
  group_role VARCHAR(32) NOT NULL DEFAULT 'member',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_group_members (user_id, group_id),
  KEY idx_ugm_group (group_id),
  KEY idx_ugm_group_role (group_id, group_role)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 3. 组权限：组员加入后继承的权限/应用授权
--    permission_code：复用全局权限码体系；应用访问用 app:use:xxx 形式表达
CREATE TABLE IF NOT EXISTS group_permissions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  group_id BIGINT UNSIGNED NOT NULL,
  permission_code VARCHAR(191) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_group_permissions (group_id, permission_code),
  KEY idx_gp_permission_code (permission_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 4. 组邀请码/注册渠道：注册时按码落到对应组并赋默认组内角色
--    max_uses：0 表示不限次数；used_count 记录已使用次数
--    expires_at：NULL 表示永不过期
--    status：active / disabled
CREATE TABLE IF NOT EXISTS group_invite_codes (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL,
  group_id BIGINT UNSIGNED NOT NULL,
  default_group_role VARCHAR(32) NOT NULL DEFAULT 'member',
  max_uses INT NOT NULL DEFAULT 0,
  used_count INT NOT NULL DEFAULT 0,
  expires_at DATETIME NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_by BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_group_invite_codes_code (code),
  KEY idx_gic_group (group_id),
  KEY idx_gic_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
