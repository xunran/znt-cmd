# Clean Core 单机部署运行手册

版本：2026-06-05  
范围：单进程 Clean Core + 单 Postgres 实例。本手册不承诺机器级 HA、跨节点故障转移、K8s 多副本或分布式队列。

## 1. 单机目标和边界

单机上线目标：

```text
1. 服务入口可保护：/healthz、/readyz、/metrics 可查，HTTP timeout/body limit 生效。
2. Postgres 是生产事实源：迁移先成功，live schema ready 后再接流量。
3. 资源可控：DB pool、agent.run admission、请求体大小、HTTP timeout 都显式配置。
4. 可恢复：进程重启、Postgres 重启、迁移失败、日志查看、备份恢复有固定流程。
```

明确不做：

```text
1. 不用单机结果宣称集群级 HA。
2. 不在单机阶段承诺机器宕机仍可用。
3. 不跳过 migration checksum；已应用 migration 文件视为不可变。
```

## 2. 必需配置

生产单机必须设置：

| 配置 | 环境变量 | 说明 |
| --- | --- | --- |
| `database_url` | `CLEAN_CORE_DATABASE_URL` | Postgres DSN，例如 `postgres://clean_core:***@127.0.0.1:5432/clean_core?sslmode=disable` |
| `service_token` | `CLEAN_CORE_SERVICE_TOKEN` | 服务 API token；不要使用示例值 |
| `model_base_url` | `CLEAN_CORE_MODEL_BASE_URL` | OpenAI-compatible/DeepSeek 等模型入口 |
| `model_api_key` | `CLEAN_CORE_MODEL_API_KEY` | 模型密钥；只放 secret/env 文件 |
| `model_name` | `CLEAN_CORE_MODEL_NAME` | 生产模型名 |

单机建议显式设置：

```text
CLEAN_CORE_ENV=production
CLEAN_CORE_READINESS_MODE=deep
CLEAN_CORE_DB_MAX_OPEN_CONNS=25
CLEAN_CORE_DB_MAX_IDLE_CONNS=10
CLEAN_CORE_HTTP_READ_HEADER_TIMEOUT_SECONDS=5
CLEAN_CORE_HTTP_READ_TIMEOUT_SECONDS=30
CLEAN_CORE_HTTP_WRITE_TIMEOUT_SECONDS=300
CLEAN_CORE_HTTP_IDLE_TIMEOUT_SECONDS=120
CLEAN_CORE_HTTP_MAX_BODY_BYTES=4194304
CLEAN_CORE_RUN_MAX_CONCURRENT=100
CLEAN_CORE_TENANT_RUN_MAX_CONCURRENT=50
CLEAN_CORE_AGENT_RUN_MAX_CONCURRENT=20
CLEAN_CORE_AGENT_RUN_EXECUTION_MODE=async
```

开发或验收时可以临时使用 `CLEAN_CORE_MODEL_PROVIDER=stub`，但真实上线必须配置真实模型 provider/base URL/key/name。

## 3. 启动方式

### 3.1 本机进程

PowerShell 示例：

```powershell
. .\local.single-node.env.example.ps1
.\.tools\go-go1.26.3\go\bin\go.exe run .\cmd\clean-core-server -migration status -migration-dir migrations
.\.tools\go-go1.26.3\go\bin\go.exe run .\cmd\clean-core-server -migration up -migration-dir migrations
.\.tools\go-go1.26.3\go\bin\go.exe run .\cmd\clean-core-server
```

Linux/systemd 主机也可以先构建二进制：

```bash
go build -o /opt/clean-core/clean-core-server ./cmd/clean-core-server
/opt/clean-core/clean-core-server -migration up -migration-dir /opt/clean-core/migrations
/opt/clean-core/clean-core-server
```

### 3.2 Docker Compose

本地单机 Compose：

```powershell
docker compose up --build postgres migrate server
```

Compose 的 `server` 已显式开启 `CLEAN_CORE_READINESS_MODE=deep`，`migrate` 成功后才启动服务。默认 token/password 仅用于本地开发，生产必须替换为 secret。

### 3.3 systemd

示例文件：

```text
deploy/systemd/clean-core.service.example
deploy/systemd/clean-core.env.example
```

推荐部署位置：

```text
/opt/clean-core/clean-core-server
/opt/clean-core/migrations
/etc/clean-core/clean-core.env
```

启用示例：

```bash
sudo cp deploy/systemd/clean-core.service.example /etc/systemd/system/clean-core.service
sudo install -d -m 0750 -o clean-core -g clean-core /etc/clean-core
sudo cp deploy/systemd/clean-core.env.example /etc/clean-core/clean-core.env
sudo editor /etc/clean-core/clean-core.env
sudo systemctl daemon-reload
sudo systemctl enable --now clean-core
```

## 4. 上线检查

每次上线前执行：

```powershell
.\.tools\go-go1.26.3\go\bin\go.exe run .\cmd\clean-core-server -migration status -migration-dir migrations
.\.tools\go-go1.26.3\go\bin\go.exe run .\cmd\clean-core-server -migration up -migration-dir migrations
Invoke-RestMethod http://127.0.0.1:8080/healthz
Invoke-RestMethod http://127.0.0.1:8080/readyz
Invoke-RestMethod http://127.0.0.1:8080/metrics
Invoke-RestMethod http://127.0.0.1:8080/v1/readiness/report -Headers @{ Authorization = "Bearer $env:CLEAN_CORE_SERVICE_TOKEN" }
Invoke-RestMethod http://127.0.0.1:8080/v1/release/go-no-go -Headers @{ Authorization = "Bearer $env:CLEAN_CORE_SERVICE_TOKEN" }
```

最小自动化验收：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\e2e_api_smoke.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\e2e_postgres_release.ps1
```

## 5. 恢复流程

### 5.1 进程重启

```bash
sudo systemctl restart clean-core
sudo journalctl -u clean-core -n 200 --no-pager
curl -fsS http://127.0.0.1:8080/readyz
```

确认：

```text
1. /readyz 返回 ready。
2. /v1/readiness/report 中 database、migration.live_schema、governance、tool.registry 为 pass。
3. /metrics 中 db_open_connections、agent_runs_running、agent_runs_rejected_total 可读。
```

### 5.2 Postgres 重启

1. 先让服务保持运行或由 systemd 自动重启。
2. 重启 Postgres。
3. 观察 `/readyz`，deep mode 下 DB 异常期间应返回 `not_ready`。
4. Postgres 恢复后重新检查 `/readyz`、`/v1/readiness/report`、`/metrics`。

### 5.3 迁移失败

不要修改已经应用过的 migration SQL 来“修复”旧库。处理顺序：

```text
1. 读取错误。如果是 checksum mismatch，先恢复原 migration 文件。
2. 需要变更 schema 时，新建下一号 migration。
3. 本地/dev 可在备份后重建数据库；生产先备份，再制定 forward migration。
4. 重新运行 migration status/up，并确认 live_schema=ready。
```

### 5.4 备份和恢复

备份：

```bash
pg_dump "$CLEAN_CORE_DATABASE_URL" --format=custom --file "clean-core-$(date +%Y%m%d%H%M%S).dump"
```

恢复到新库：

```bash
createdb clean_core_restore
pg_restore --clean --if-exists --dbname "$CLEAN_CORE_DATABASE_URL" clean-core.dump
/opt/clean-core/clean-core-server -migration status -migration-dir /opt/clean-core/migrations
```

恢复后必须跑 `/readyz`、`/v1/readiness/report` 和一次 `agent.run` smoke。

## 6. 日志和回滚开关

日志：

```text
systemd: journalctl -u clean-core -f
Docker:  docker compose logs -f server
本机进程: stdout/stderr
```

回滚/禁用：

```text
CLEAN_CORE_DISABLED_AGENT_IDS=agent_a,agent_b
CLEAN_CORE_DISABLED_TOOL_IDS=tool_a,tool_b
CLEAN_CORE_DISABLE_HANDOFF=true
CLEAN_CORE_DISABLE_EXTERNAL_TOOLS_INVOKE=true
```

这些开关会进入 readiness/release go-no-go 的 release switch 信号。启用后要记录原因和恢复时间。
