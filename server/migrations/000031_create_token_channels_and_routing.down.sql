-- 000031 回滚：移除 token_models 路由列 + 删除 token_channels 表

ALTER TABLE token_models
  DROP KEY idx_token_models_channel,
  DROP COLUMN upstream_model,
  DROP COLUMN channel_id;

DROP TABLE IF EXISTS token_channels;
