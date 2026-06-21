-- 000038 聊天工作台 Agent（=角色/人设）+ Agent 绑定 skill/plugin 关系表
-- Agent 即角色，免费资源；区分官方上架（official）与用户自建（user）。
-- 绑定表与 agents 放同一迁移内（外键级联：删 Agent 自动清绑定）。

-- Agent 主表
CREATE TABLE IF NOT EXISTS agents (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NULL COMMENT '官方预设唯一编码；用户自建为 NULL',
  name VARCHAR(128) NOT NULL COMMENT 'Agent 名称',
  description VARCHAR(512) NOT NULL DEFAULT '' COMMENT '描述',
  avatar VARCHAR(512) NOT NULL DEFAULT '' COMMENT '头像 URL',
  owner_type VARCHAR(16) NOT NULL DEFAULT 'official' COMMENT '归属类型：official 官方 / user 用户自建',
  owner_user_id BIGINT UNSIGNED NULL COMMENT '用户自建时的拥有者用户 ID，官方为空',
  system_prompt TEXT NOT NULL COMMENT '系统提示词（人设）',
  default_model_code VARCHAR(128) NOT NULL COMMENT '默认逻辑模型，指向 token_models.logical_model_code',
  status VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT '状态：active 启用 / inactive 停用',
  sort_order INT NOT NULL DEFAULT 0 COMMENT '排序权重，越小越靠前',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_agents_code (code),
  KEY idx_agents_owner (owner_type, owner_user_id),
  KEY idx_agents_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='聊天工作台 Agent（角色/人设）';

-- Agent ↔ Skill 绑定关系
CREATE TABLE IF NOT EXISTS agent_skill_bindings (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  agent_id BIGINT UNSIGNED NOT NULL COMMENT '所属 Agent ID',
  skill_id BIGINT UNSIGNED NOT NULL COMMENT '绑定的 Skill ID',
  enabled TINYINT(1) NOT NULL DEFAULT 1 COMMENT '是否启用：1 启用 / 0 停用',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_agent_skill (agent_id, skill_id),
  KEY idx_agent_skill_skill (skill_id),
  CONSTRAINT fk_agent_skill_agent FOREIGN KEY (agent_id) REFERENCES agents (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Agent 绑定 Skill 关系';

-- Agent ↔ Plugin 绑定关系
CREATE TABLE IF NOT EXISTS agent_plugin_bindings (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  agent_id BIGINT UNSIGNED NOT NULL COMMENT '所属 Agent ID',
  plugin_id BIGINT UNSIGNED NOT NULL COMMENT '绑定的 Plugin ID',
  enabled TINYINT(1) NOT NULL DEFAULT 1 COMMENT '是否启用：1 启用 / 0 停用',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_agent_plugin (agent_id, plugin_id),
  KEY idx_agent_plugin_plugin (plugin_id),
  CONSTRAINT fk_agent_plugin_agent FOREIGN KEY (agent_id) REFERENCES agents (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Agent 绑定 Plugin 关系';
