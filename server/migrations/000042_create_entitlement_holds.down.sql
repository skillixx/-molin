-- 000042 回滚：先删 entitlement_holds 表，再删 user_entitlements.quota_reserved 列。
-- 顺序：DROP TABLE 在前（独立表，无外键依赖），ALTER DROP COLUMN 在后。
DROP TABLE IF EXISTS entitlement_holds;

ALTER TABLE user_entitlements
  DROP COLUMN quota_reserved;
