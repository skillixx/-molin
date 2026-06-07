-- Week 4 验收测试种子数据：插入不同 status 的应用记录，
-- 用于验证用户端 GET /api/marketplace/apps/:id 的可见性边界
-- （仅 status=active 可见，其余统一返回"不存在或未上架"，不暴露真实状态）。
-- 使用 INSERT IGNORE，可重复执行不报错。

INSERT IGNORE INTO applications
  (id, code, name, type, description, icon_url, callback_url, adapter_config_json, status)
VALUES
  (101, 'qa-app-active',   'QA测试应用-已上架', 'netdisk', '用于验证用户端可见性：active 状态应可查看', NULL, NULL, NULL, 'active'),
  (102, 'qa-app-draft',    'QA测试应用-草稿',   'netdisk', '用于验证用户端可见性：draft 状态应不可见',   NULL, NULL, NULL, 'draft'),
  (103, 'qa-app-inactive', 'QA测试应用-已下架', 'netdisk', '用于验证用户端可见性：inactive 状态应不可见', NULL, NULL, NULL, 'inactive'),
  (104, 'qa-app-archived', 'QA测试应用-已归档', 'netdisk', '用于验证用户端可见性：archived 状态应不可见', NULL, NULL, NULL, 'archived');
