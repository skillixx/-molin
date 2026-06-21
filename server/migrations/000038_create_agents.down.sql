-- 000038 回滚：先删绑定表（含外键），再删 agents 主表
DROP TABLE IF EXISTS agent_plugin_bindings;
DROP TABLE IF EXISTS agent_skill_bindings;
DROP TABLE IF EXISTS agents;
