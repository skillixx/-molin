-- 000034 平台 API Key（sk）表 api_keys
-- 让外部程序 / Agent 凭平台 sk 调用模型门面，沿用 Refresh Token「只存 HMAC、明文只回一次」安全模式。
-- DB 只存 HMAC-SHA256(明文, API_KEY_HMAC_SECRET)，绝不存明文；明文仅创建时返回一次。
CREATE TABLE IF NOT EXISTS api_keys (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id       BIGINT UNSIGNED NOT NULL,
  key_prefix    VARCHAR(32)  NOT NULL,                       -- 展示用前缀，如 sk-molin-AbCd（不可反推）
  key_hash      VARCHAR(128) NOT NULL,                       -- HMAC-SHA256(明文)，全局唯一
  name          VARCHAR(128) NOT NULL DEFAULT '',            -- 用户备注名
  billing_mode  VARCHAR(16)  NOT NULL DEFAULT 'postpaid',    -- postpaid(钱包) / prepaid(套餐额度)
  source_id     BIGINT UNSIGNED NULL,                        -- prepaid=entitlement_id；postpaid=NULL
  model_scope   VARCHAR(512) NOT NULL DEFAULT '',            -- 逗号分隔 logical_model_code；空=不限
  status        VARCHAR(16)  NOT NULL DEFAULT 'active',      -- active / revoked
  last_used_at  DATETIME     NULL,                           -- 最近一次使用时间，命中后惰性更新
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_api_keys_hash (key_hash),
  KEY idx_api_keys_user (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
