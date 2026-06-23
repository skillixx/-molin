-- 000045 回滚：删 agents.category_code 列 + drop 分类字典表

ALTER TABLE agents
  DROP KEY idx_agents_category,
  DROP COLUMN category_code;

DROP TABLE IF EXISTS agent_categories;
