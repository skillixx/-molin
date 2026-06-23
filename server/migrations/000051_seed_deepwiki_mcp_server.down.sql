-- 000051 回滚：删除 DeepWiki 示例 MCP server（绑定/工具快照经 FK CASCADE 一并清理）
DELETE FROM mcp_servers WHERE code = 'deepwiki';
