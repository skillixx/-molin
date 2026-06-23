-- 回滚 Agent 定向可见性：删两列，恢复全员可见基线。
ALTER TABLE agents
  DROP COLUMN target_audience_json,
  DROP COLUMN visible_scope;
