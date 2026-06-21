-- 000039 聊天工作台 Skill（平台内置能力，免费）
-- 如联网搜索/读文档；门面通过 handler_key 路由到内置实现。本期不含代码执行（D4）。
-- skill 均为官方上架，用户不能自建/上传。

CREATE TABLE IF NOT EXISTS skills (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL COMMENT '唯一编码',
  name VARCHAR(128) NOT NULL COMMENT '名称',
  description VARCHAR(512) NOT NULL DEFAULT '' COMMENT '描述',
  category VARCHAR(64) NOT NULL DEFAULT '' COMMENT '分类',
  tool_schema_json JSON NOT NULL COMMENT 'function calling 工具定义（OpenAI tools 格式）',
  handler_key VARCHAR(64) NOT NULL COMMENT '内置实现路由键（门面 dispatch，如 web_search/doc_read）',
  status VARCHAR(16) NOT NULL DEFAULT 'active' COMMENT '状态：active 启用 / inactive 停用',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (id),
  UNIQUE KEY uk_skills_code (code),
  KEY idx_skills_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='聊天工作台 Skill（平台内置能力）';
