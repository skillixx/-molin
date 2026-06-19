-- 000028 组绑定角色表 group_roles
-- 组与全局角色的多对多绑定。用户继承所在组绑定的角色（GetUserRoleIDs 合并），
-- 用于商品访问/定价授权（A 版：只影响商品，不进入权限码判定）。
CREATE TABLE IF NOT EXISTS group_roles (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  group_id BIGINT UNSIGNED NOT NULL,
  role_id  BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_group_roles (group_id, role_id),
  KEY idx_group_roles_role_id (role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
