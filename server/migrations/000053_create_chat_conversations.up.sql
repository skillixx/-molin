-- 000053 会话持久化（ChatGPT 式有状态会话）：会话表 + 消息表
-- 设计目标：聊天记忆/上下文连贯、滚动摘要压缩、新建会话、用户信息强隔离。
-- agent_id 为空 = 普通聊天会话；非空 = Agent 会话。一切读写按 user_id 隔离。

-- 会话表
CREATE TABLE IF NOT EXISTS chat_conversations (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL COMMENT '所属用户（强隔离维度）',
  agent_id BIGINT UNSIGNED NULL COMMENT 'NULL=普通聊天；非空=Agent 会话，指向 agents.id',
  title VARCHAR(255) NOT NULL DEFAULT '' COMMENT '会话标题（默认取首条用户消息截断）',
  model_code VARCHAR(128) NOT NULL DEFAULT '' COMMENT '会话使用的逻辑模型名；普通聊天必填，Agent 会话空则用 Agent 默认模型',
  summary MEDIUMTEXT NULL COMMENT '滚动压缩的历史摘要（早期消息被压缩成此摘要，作为长期记忆）',
  summarized_until_id BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '摘要已覆盖到的最后一条 message id（水位线）；其后的消息为原文上下文',
  message_count INT NOT NULL DEFAULT 0 COMMENT '消息总数（含已被摘要的）',
  last_message_at DATETIME NULL COMMENT '最后一条消息时间（列表排序用）',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (id),
  KEY idx_chat_conv_user_time (user_id, last_message_at),
  KEY idx_chat_conv_user_agent (user_id, agent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='聊天会话（有状态记忆）';

-- 消息表
CREATE TABLE IF NOT EXISTS chat_messages (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  conversation_id BIGINT UNSIGNED NOT NULL COMMENT '所属会话',
  user_id BIGINT UNSIGNED NOT NULL COMMENT '冗余所属用户（鉴权隔离，免 join）',
  role VARCHAR(16) NOT NULL COMMENT 'user / assistant / tool / system',
  content MEDIUMTEXT NULL COMMENT '消息文本内容',
  tool_calls JSON NULL COMMENT 'assistant 发起的 tool_calls 原文（可空）',
  tool_call_id VARCHAR(128) NOT NULL DEFAULT '' COMMENT 'role=tool 时归属的 tool_call_id（可空）',
  token_est INT NOT NULL DEFAULT 0 COMMENT 'token 估算值（上下文预算/压缩触发用，启发式）',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  PRIMARY KEY (id),
  KEY idx_chat_msg_conv (conversation_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='聊天消息';
