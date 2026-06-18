-- 修复 000016_seed_group_manage_permission 写入的 group:manage / scope:all 权限名乱码。
-- 根因：scripts/migrate.sh 的 DB_URL 缺少 charset=utf8mb4，golang-migrate 的 MySQL 驱动
--       默认以 latin1 连接，UTF-8 中文字节被按 latin1 解释后再转存进 utf8mb4 列，
--       产生双重编码（mojibake）。同表其余权限名经正确路径写入，未受影响。
-- 修复方式：使用 _utf8mb4 0x... 十六进制字面量直接写入正确字节，绕开连接字符集转换，
--          无论本次 migrate 连接是 latin1 还是 utf8mb4 都能正确落库。
-- 注：根因（migrate.sh 缺 charset）属运维 scripts/ 范围，需运维另行修正连接串以防复发。

-- 用户分组管理
UPDATE permissions SET name = _utf8mb4 0xE794A8E688B7E58886E7BB84E7AEA1E79086 WHERE code = 'group:manage';
-- 数据范围不受限
UPDATE permissions SET name = _utf8mb4 0xE695B0E68DAEE88C83E59BB4E4B88DE58F97E99990 WHERE code = 'scope:all';
