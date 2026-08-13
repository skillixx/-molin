-- 修复测试环境历史运维写入造成的百炼渠道名称乱码。
-- 使用精确的渠道编码和错误字节双重限定，避免覆盖管理员后来设置的合法名称。
-- 正确名称也使用十六进制 UTF-8 常量生成，防止迁移客户端字符集再次破坏中文。
UPDATE token_channels
SET name = CONVERT(0xE799BEE782BC20426966726F7374 USING utf8mb4)
WHERE code = 'bailian'
  AND HEX(name) = 'C3A7E284A2C2BEC3A7E2809AC2BC20426966726F7374';
