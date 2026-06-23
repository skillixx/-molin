-- 000051 seed 示例 MCP server：DeepWiki（远程公共 MCP，Streamable HTTP，免鉴权，只读）
-- 来源：mcp.so / mcp.deepwiki.com —— 按 GitHub 仓库查文档/问答（工具 read_wiki_structure/read_wiki_contents/ask_question）。
-- 选它的原因：MCP 接入 v1 仅支持远程 https Streamable HTTP；DeepWiki 公网 https + 免鉴权 + 只读，低风险，适合首个示例工具源。
-- 无鉴权 → auth_config_encrypted 留 NULL（响应 has_auth=false）。
-- 默认 status=inactive：seed 只建 server 记录，工具需运营在管理端 discover + 逐个审核启用、再置 active 后才会进编排（防 tool poisoning）。
-- 幂等：INSERT IGNORE，按 code 唯一，可重复执行。
-- ⚠️ 运行时外呼受 SSRF 域名白名单约束：若 PLUGIN_DOMAIN_WHITELIST 非空，需包含 mcp.deepwiki.com 才能 discover 成功。

INSERT IGNORE INTO mcp_servers (code, name, description, endpoint_url, timeout_ms, is_paid, daily_limit, status)
VALUES (
  'deepwiki',
  'DeepWiki',
  '按 GitHub 仓库查文档/问答（只读，远程公共 MCP）；调用需传 repoName，如 facebook/react',
  'https://mcp.deepwiki.com/mcp',
  20000,
  0,
  0,
  'inactive'
);
