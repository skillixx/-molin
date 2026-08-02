# Bifrost Docker 部署与运维文档

> 文档状态：测试环境实施指南。
>
> 适用范围：只部署 Bifrost，不部署或修改墨灵 Go API、MySQL、Redis、RabbitMQ、MinIO 和前端。
>
> 版本基线：截至 2026-07-30，POC 候选版本为 `maximhq/bifrost:v1.6.6`。正式执行前必须再次核对官方 Release，并记录镜像摘要，禁止使用浮动 `latest`。
>
> 安全要求：本文档和 Git 中禁止出现百炼 SK、OpenRouter Key、Bifrost 加密密钥或其他真实凭据。

## 1. 部署目标

测试环境使用一台 Linux 服务器运行：

```text
127.0.0.1:18080
        |
        v
  bifrost-lb (Nginx)
        |
        +-- bifrost-1:8080
        |
        `-- bifrost-2:8080
```

目标：

1. 两个 Bifrost 实例使用同一个只读 `config.json`。
2. 两个实例分别保存运行数据，不能共享 SQLite 文件或同一可写数据目录。
3. 统一通过 `127.0.0.1:18080` 访问，实例端口不暴露到宿主机和公网。
4. 支持普通响应、SSE 流式响应、健康检查、单实例故障和滚动重启。
5. 上游密钥只通过受限环境文件注入。

限制：同一台服务器上的双容器只能防止单个进程或容器故障，不能防止宿主机、Docker、磁盘或机房故障，不属于跨机器高可用。

## 2. 官方部署边界

Bifrost OSS 多节点的关键约束：

- 每个节点会把 Provider、Key 和路由等配置加载到本机内存。
- OSS 多节点不能依赖 PostgreSQL 自动同步内存配置。
- 多节点应将 `config_store.enabled` 设置为 `false`，使用相同的 `config.json` 作为事实源。
- 配置变更后必须将同一版本分发给全部节点，再逐节点滚动重启。
- 密钥必须使用 `env.变量名` 引用，不得写入 `config.json`。

官方资料：

- Docker 快速部署：<https://docs.getbifrost.ai/quickstart/gateway/setting-up>
- `config.json` 说明：<https://docs.getbifrost.ai/deployment-guides/config-json>
- 多节点部署：<https://docs.getbifrost.ai/deployment-guides/how-to/multinode>
- 自定义 Provider：<https://docs.getbifrost.ai/providers/custom-providers>
- 官方 Release：<https://github.com/maximhq/bifrost/releases>

## 3. 前置条件

服务器需要：

```text
Linux x86_64
Docker Engine
可访问 Docker Hub
可访问百炼和 OpenRouter API
至少 4 核 CPU、4 GiB 可用内存
至少 10 GiB 可用磁盘
```

当前墨灵测试服务器的 CPU、内存和磁盘满足 Bifrost 双实例 POC，但部署前仍需确认其他服务的实际资源占用。

需要人工准备：

```text
百炼测试 SK
OpenRouter 测试 Key
两个上游的测试费用上限
允许执行真实上游最小请求的授权
```

## 4. 目录规划

```text
/opt/bifrost/
  config.json
  nginx.conf
  data/
    node-1/
    node-2/

/opt/molin-secrets/
  bifrost.env
```

创建目录：

```bash
sudo install -d -m 755 /opt/bifrost
sudo install -d -m 750 /opt/bifrost/data/node-1
sudo install -d -m 750 /opt/bifrost/data/node-2
sudo install -d -m 700 /opt/molin-secrets
```

## 5. 密钥文件

由项目负责人在服务器终端创建：

```bash
sudo nano /opt/molin-secrets/bifrost.env
```

填写以下变量。等号右侧只能在服务器终端中填写，不得粘贴到聊天或文档：

```dotenv
BIFROST_ENCRYPTION_KEY=<独立的32字节随机密钥>
BAILIAN_API_KEY=<百炼测试SK>
OPENROUTER_API_KEY=<OpenRouter测试Key>
```

可以使用以下命令生成 Bifrost 独立加密密钥：

```bash
openssl rand -base64 32
```

设置权限：

```bash
sudo chown root:root /opt/molin-secrets/bifrost.env
sudo chmod 600 /opt/molin-secrets/bifrost.env
```

只检查变量名称，不显示变量值：

```bash
sudo sed -n 's/=.*$/=[REDACTED]/p' /opt/molin-secrets/bifrost.env
```

## 6. Bifrost 配置

保存为 `/opt/bifrost/config.json`：

```json
{
  "$schema": "https://www.getbifrost.ai/schema",
  "encryption_key": "env.BIFROST_ENCRYPTION_KEY",
  "client": {
    "drop_excess_requests": true,
    "enable_logging": false
  },
  "providers": {
    "openrouter": {
      "keys": [
        {
          "name": "openrouter-test",
          "value": "env.OPENROUTER_API_KEY",
          "models": ["cohere/north-mini-code:free"],
          "weight": 1
        }
      ]
    },
    "bailian": {
      "keys": [
        {
          "name": "bailian-test",
          "value": "env.BAILIAN_API_KEY",
          "models": ["qwen-turbo"],
          "weight": 1
        }
      ],
      "network_config": {
        "base_url": "https://dashscope.aliyuncs.com/compatible-mode",
        "default_request_timeout_in_seconds": 60,
        "max_retries": 1
      },
      "custom_provider_config": {
        "base_provider_type": "openai",
        "allowed_requests": {
          "chat_completion": true,
          "chat_completion_stream": true
        },
        "request_path_overrides": {
          "chat_completion": "/v1/chat/completions",
          "chat_completion_stream": "/v1/chat/completions"
        }
      }
    }
  },
  "config_store": {
    "enabled": false
  }
}
```

说明：

- `$schema` 只为编辑器提供字段补全和校验，不是推理 API，可在离线环境删除。
- `config_store.enabled=false` 表示运行配置以文件为准，不能依赖 Web UI 修改配置。
- `enable_logging=false` 是 POC 初始安全值，避免 Bifrost 保存完整提示词和响应。
- 初始模型只用于连通性验证，后续必须根据上游模型列表和计量测试发布正式白名单。
- Bifrost 对自定义 Provider 的模型前缀和返回字段必须以锁定版本实测为准。

设置只读权限：

```bash
sudo chown root:root /opt/bifrost/config.json
sudo chmod 644 /opt/bifrost/config.json
```

## 7. Nginx 负载均衡配置

保存为 `/opt/bifrost/nginx.conf`：

```nginx
events {
    worker_connections 4096;
}

http {
    upstream bifrost_nodes {
        least_conn;
        server bifrost-1:8080 max_fails=3 fail_timeout=10s;
        server bifrost-2:8080 max_fails=3 fail_timeout=10s;
        keepalive 128;
    }

    server {
        listen 8080;

        location / {
            proxy_pass http://bifrost_nodes;
            proxy_http_version 1.1;

            # SSE 响应必须立即转发，不能被代理缓冲或缓存。
            proxy_buffering off;
            proxy_cache off;
            proxy_read_timeout 600s;
            proxy_send_timeout 600s;

            proxy_set_header Host $host;
            proxy_set_header X-Request-ID $http_x_request_id;
            add_header X-Accel-Buffering no;
        }
    }
}
```

设置权限：

```bash
sudo chown root:root /opt/bifrost/nginx.conf
sudo chmod 644 /opt/bifrost/nginx.conf
```

## 8. 拉取并锁定镜像

拉取固定版本：

```bash
sudo docker pull maximhq/bifrost:v1.6.6
sudo docker pull nginx:1.27-alpine
```

记录镜像摘要：

```bash
sudo docker image inspect maximhq/bifrost:v1.6.6 \
  --format '{{json .RepoDigests}}'
```

POC 通过后，将实际摘要写入部署清单。后续部署优先使用 `镜像@sha256:摘要`，避免相同标签内容变化。

## 9. 创建内部 Docker 网络

```bash
sudo docker network inspect bifrost-net >/dev/null 2>&1 || \
  sudo docker network create bifrost-net
```

该网络只用于三个 Bifrost 相关容器通信，不需要加入墨灵现有 Docker 网络。

## 10. 启动 Bifrost 节点

### 10.1 节点 1

```bash
sudo docker run -d \
  --name bifrost-1 \
  --restart unless-stopped \
  --network bifrost-net \
  --env-file /opt/molin-secrets/bifrost.env \
  -e APP_HOST=0.0.0.0 \
  -e APP_PORT=8080 \
  -e LOG_LEVEL=info \
  -e LOG_STYLE=json \
  -e GOMEMLIMIT=1800MiB \
  -e GOGC=200 \
  --memory 2g \
  --cpus 2 \
  --ulimit nofile=65536:65536 \
  -v /opt/bifrost/data/node-1:/app/data \
  -v /opt/bifrost/config.json:/app/data/config.json:ro \
  maximhq/bifrost:v1.6.6
```

### 10.2 节点 2

```bash
sudo docker run -d \
  --name bifrost-2 \
  --restart unless-stopped \
  --network bifrost-net \
  --env-file /opt/molin-secrets/bifrost.env \
  -e APP_HOST=0.0.0.0 \
  -e APP_PORT=8080 \
  -e LOG_LEVEL=info \
  -e LOG_STYLE=json \
  -e GOMEMLIMIT=1800MiB \
  -e GOGC=200 \
  --memory 2g \
  --cpus 2 \
  --ulimit nofile=65536:65536 \
  -v /opt/bifrost/data/node-2:/app/data \
  -v /opt/bifrost/config.json:/app/data/config.json:ro \
  maximhq/bifrost:v1.6.6
```

两个节点不使用 `-p`，因此不会直接开放宿主机端口。

## 11. 启动内部负载均衡

```bash
sudo docker run -d \
  --name bifrost-lb \
  --restart unless-stopped \
  --network bifrost-net \
  -p 127.0.0.1:18080:8080 \
  -v /opt/bifrost/nginx.conf:/etc/nginx/nginx.conf:ro \
  nginx:1.27-alpine
```

安全要求：

- 只能绑定 `127.0.0.1:18080`，禁止使用 `0.0.0.0:18080` 暴露到公网。
- 不在安全组或防火墙开放 `18080`。
- 如需临时查看 Web UI，应使用 SSH 本地端口转发，不直接开放公网端口。

## 12. 启动验证

检查容器：

```bash
sudo docker ps --filter name=bifrost
```

检查节点自身：

```bash
sudo docker exec bifrost-1 \
  wget -qO- http://127.0.0.1:8080/health

sudo docker exec bifrost-2 \
  wget -qO- http://127.0.0.1:8080/health
```

检查负载均衡入口：

```bash
curl --fail --silent --show-error \
  http://127.0.0.1:18080/health
```

查看日志：

```bash
sudo docker logs --tail 100 bifrost-1
sudo docker logs --tail 100 bifrost-2
sudo docker logs --tail 100 bifrost-lb
```

日志检查要求：不得出现任何完整 SK、Authorization Header、完整提示词或完整模型响应。

## 13. 最小推理测试

### 13.1 OpenRouter

```bash
curl --fail-with-body http://127.0.0.1:18080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "openrouter/cohere/north-mini-code:free",
    "messages": [
      {"role": "user", "content": "Reply only OK"}
    ],
    "max_tokens": 1,
    "stream": false
  }'
```

### 13.2 阿里云百炼

```bash
curl --fail-with-body http://127.0.0.1:18080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "bailian/qwen-turbo",
    "messages": [
      {"role": "user", "content": "请只回答OK"}
    ],
    "max_tokens": 1,
    "stream": false
  }'
```

注意：上面模型前缀是 POC 候选写法。如果锁定版本返回模型解析错误，应先查询 Bifrost 模型目录和 Provider 配置，不得通过删除 Provider 边界或改写用户账单模型来掩盖错误。

## 14. SSE 流式测试

```bash
curl --no-buffer http://127.0.0.1:18080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "bailian/qwen-turbo",
    "messages": [
      {"role": "user", "content": "请用一句话介绍成都"}
    ],
    "max_tokens": 32,
    "stream": true
  }'
```

验收内容：

- 首个分片能够及时到达，不被 Nginx 缓冲。
- 流正常结束并包含可识别终帧。
- Usage 字段能够被读取或归一化。
- 客户端中断后连接能够在合理时间内释放。

## 15. 单节点故障演练

停止节点 1：

```bash
sudo docker stop bifrost-1
curl --fail http://127.0.0.1:18080/health
```

恢复节点 1，再停止节点 2：

```bash
sudo docker start bifrost-1
sudo docker stop bifrost-2
curl --fail http://127.0.0.1:18080/health
```

恢复全部节点：

```bash
sudo docker start bifrost-2
```

至少连续执行普通请求和 SSE 请求，确认单节点退出不会让统一入口整体失效。

## 16. 配置更新与滚动重启

每次配置变更必须生成版本和摘要：

```bash
sudo cp /opt/bifrost/config.json \
  /opt/bifrost/config.json.backup-$(date +%Y%m%d%H%M%S)

sha256sum /opt/bifrost/config.json
```

更新顺序：

```text
校验新 config.json
  -> 重建或重启 bifrost-1
  -> 验证节点 1 和负载均衡入口
  -> 重建或重启 bifrost-2
  -> 验证节点 2 和负载均衡入口
  -> 执行最小推理与 SSE 测试
```

如果只修改 `config.json`，节点不会自动可靠同步新内存配置，必须逐节点重启。

## 17. 停止与删除

停止但保留容器：

```bash
sudo docker stop bifrost-lb bifrost-1 bifrost-2
```

重新启动：

```bash
sudo docker start bifrost-1 bifrost-2 bifrost-lb
```

删除容器但保留配置、密钥和数据：

```bash
sudo docker rm -f bifrost-lb bifrost-1 bifrost-2
```

禁止在未备份且未确认的情况下删除 `/opt/bifrost/data`、`config.json` 或密钥文件。

## 18. 回滚

### 18.1 配置回滚

1. 恢复上一份已验证的 `config.json.backup-*`。
2. 对两个 Bifrost 节点逐个重启。
3. 每次只重启一个节点，并通过统一入口执行健康和推理测试。
4. 保留失败配置、摘要和日志用于排查，不覆盖证据。

### 18.2 镜像回滚

1. 拉取或确认上一已验证的固定镜像摘要仍在本机。
2. 先使用旧镜像重建节点 1 并验证。
3. 再使用旧镜像重建节点 2。
4. 禁止使用 `latest` 猜测旧版本。

## 19. 跨服务器多节点

真正的机器级高可用需要至少两台 Linux：

```text
内部 SLB / Nginx / HAProxy
  +-- Linux A：bifrost-1
  `-- Linux B：bifrost-2
```

要求：

- 两台机器使用相同的 Bifrost 镜像摘要。
- 两台机器使用内容相同且摘要一致的 `config.json`。
- 两台机器使用同名环境变量，但密钥由各自主机安全注入。
- Bifrost 端口只允许内部负载均衡器访问。
- 配置由 CI、Ansible 或受控脚本统一分发，然后滚动重启。
- 不允许在任意节点的 Web UI 临时修改配置，避免节点漂移。
- 日志和指标发送到统一观测系统，不能只保存在单机 SQLite。

如果要求通过 Bifrost UI/API 动态修改配置并实时同步全部节点，应单独评估 Bifrost Enterprise 集群能力，不能将 OSS 数据库配置误认为实时集群同步。

## 20. 验收清单

```text
[x] 使用固定 Bifrost 版本和镜像摘要
[x] config.json 不包含密钥明文
[x] bifrost.env 权限为 600
[x] config_store.enabled=false
[x] 两个节点使用不同可写数据目录
[x] 两个节点均不映射宿主机端口
[x] 统一入口只绑定 127.0.0.1:18080
[x] 两个节点 /health 均通过
[x] 统一入口 /health 通过
[ ] 百炼最小请求通过
[ ] OpenRouter 最小请求通过
[ ] SSE 不被 Nginx 缓冲
[ ] Usage 字段完成记录和核对
[x] 停止任意一个节点后统一入口仍可用
[x] 两个节点恢复后状态正常
[x] 日志中没有密钥、Authorization 和完整提示词
[ ] 配置回滚演练通过
[ ] 镜像回滚步骤已经验证
```

全部通过只能说明 Bifrost Docker POC 可用，不代表墨灵 AI 网关、用户计费、内容安全、钱包结算或生产商业能力已经完成。

## 21. 测试环境实际部署记录

部署日期：2026-07-30。

本次部署只新增 Bifrost 相关目录、Docker 网络和容器，没有修改墨灵 API、数据库、Redis、RabbitMQ、MinIO、前后台或现有 Nginx。

由于测试服务器现有服务统一位于当前部署用户目录，本次实际路径调整为：

```text
运行配置：~/molin/bifrost/config.json
负载均衡：~/molin/bifrost/nginx.conf
节点数据：~/molin/bifrost/data/node-1、node-2
密钥文件：~/molin/secrets/bifrost.env
```

实际容器：

```text
bifrost-1：maximhq/bifrost:v1.6.6
bifrost-2：maximhq/bifrost:v1.6.6
bifrost-lb：nginx:1.27-alpine
```

部署证据：

```text
Bifrost 镜像摘要：sha256:0c0e6f498944d2896867fd9a5e51a79bb9cc82b845763fc4ababe0a6d2ab4402
配置 SHA256：7c80855f3a1767e32a2e0776e851997112f86874d3287370245af99898881cee
统一入口：http://127.0.0.1:18080
bifrost-1：running、healthy、restart=unless-stopped
bifrost-2：running、healthy、restart=unless-stopped
bifrost-lb：running、restart=unless-stopped
统一入口健康检查：HTTP 200
停止 bifrost-1：统一入口 HTTP 200
停止 bifrost-2：统一入口 HTTP 200
敏感日志模式扫描：两个 Bifrost 节点均为 0
```

当前密钥状态：

```text
BIFROST_ENCRYPTION_KEY：已在服务器生成并配置，文档不保存明文
BAILIAN_API_KEY：已配置，文档不保存明文
OPENROUTER_API_KEY：已配置，文档不保存明文
```

2026-08-03 复核时，当前配置包含 `bailian` 和 `openrouter` 两个 Provider，本地配置与服务器配置 SHA256 一致，两个 Bifrost 节点和统一入口健康。2026-07-30 已分别取得百炼和 OpenRouter 非流式真实推理及 Usage 返回证据；该历史证据不替代 Phase 1 要求的最新普通响应、SSE、错误结构、计量契约、单节点故障和延迟对照验收。
