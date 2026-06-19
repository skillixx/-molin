-- 回滚 000029
-- 仅删除基础角色本身；若已被组绑定（group_roles）或分配给用户（user_roles），
-- 请先解绑/解除分配后再回滚，避免残留孤立引用。
DELETE FROM roles WHERE code = 'registered_user';
