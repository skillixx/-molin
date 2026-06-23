-- 000045 Agent 分类字典 + agents 加分类列（第三阶段最小版）
-- 分类只影响"怎么展示"（前端分类导航），不参与计费/可见性判定。
-- 本期只 seed 固定 4 类（办公/学习/商务/娱乐），不做管理端 CRUD（契约 §7）。

-- 分类字典表（运营元数据：名称/图标/排序）
CREATE TABLE IF NOT EXISTS agent_categories (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL COMMENT '分类编码，唯一，如 office/study/business/entertainment',
  name VARCHAR(64) NOT NULL COMMENT '展示名称，如 办公/学习/商务/娱乐',
  icon VARCHAR(128) NOT NULL DEFAULT '' COMMENT '图标标识/URL（前端展示用，可空）',
  sort_order INT NOT NULL DEFAULT 0 COMMENT '排序，越小越靠前',
  status VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT 'active 启用 / inactive 停用',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_agent_categories_code (code),
  KEY idx_agent_categories_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Agent 分类字典';

-- seed 初始 4 个分类（INSERT IGNORE 幂等）
INSERT IGNORE INTO agent_categories (code, name, sort_order) VALUES
  ('office',        '办公', 1),
  ('study',         '学习', 2),
  ('business',      '商务', 3),
  ('entertainment', '娱乐', 4);

-- agents 加分类列（软关联，指向 agent_categories.code；NULL=未分类，向后兼容）
ALTER TABLE agents
  ADD COLUMN category_code VARCHAR(64) NULL COMMENT '所属分类，指向 agent_categories.code；NULL=未分类' AFTER status,
  ADD KEY idx_agents_category (category_code);
