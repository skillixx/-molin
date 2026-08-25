-- 000069 采用事实保留式回滚。
-- 价格版本、SKU、Quote、快照和消费关系属于财务审计事实，down 不删除、不缩列、不改写历史金额。
-- 应用回退时图片流量、正式模型发布和真实钱包计费继续关闭；物理清理由独立高风险 Contract Migration 承担。
SELECT 1 AS image_gateway_g2_pricing_schema_retained;
