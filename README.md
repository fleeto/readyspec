# ReadySpec

**Linux 软件部署前环境需求定义、静态预检与现场就绪度门禁工具**  
*Pre-deployment Linux environment specification, verification, and readiness gate tool*

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](./LICENSE)
[![Python 3.12+](https://img.shields.io/badge/Python-3.12%2B-3776AB.svg)](./pyproject.toml)

---

## 核心定位

ReadySpec 解决企业级软件交付中的一个高频痛点：**研发清楚软件需要什么样的系统环境，现场部署/运维需要一套离线可携、零目标机常驻代理、证据可信、结论机器可读的部署前检查门禁。**

### 核心特性

- **双角色闭环工作流**：
  - **研发 / 交付方案作者**：在交互式方案工作台中按业务角色（如 `web` / `db`）组装 9 大领域检查项、编写带离线沙盘自测的方案内自定义 Shell 检查、指定允许现场调整的参数边界，并导出带 **RFC 8785 (JCS) SHA-256 语义摘要** 的自包含 YAML 方案。
  - **现场部署 / 运维工程师**：导入自包含方案、填入主机清单（原生支持 **Ansible INI / YAML** 与 **快速 IP 列表**）、完成允许范围内的现场覆盖与特权确认，输出统一证据深度合同（`L0–L3`）与就绪度结论（`READY / NOT_READY / UNKNOWN`）。
- **四幕交互式 TUI + 非交互 CI/CD CLI 双入口**：
  - 针对 `140 / 100 / 80` 列终端自适应布局，关闭终端 `IXON` 流控避免 `Ctrl+S` 锁屏，全局快捷键与文本输入严格隔离。
  - CLI 提供语义化门禁退出码（`0` 就绪放行，`10~15` 精确区分未就绪、证据不足、安全拒绝、参数冲突与控制面锁冲突）。
- **Goss `v0.4.9` 智能合并编译器**：
  - 三级线性参数解析（`主机覆盖 > 组覆盖 > 方案设定 > 模板默认`），自定义脚本参数仅通过只读环境变量 `$RS_PARAM_<KEY>` 注入，彻底阻断命令拼接注入。
  - 同一主机上指向相同路径/端口的 Goss 原生资源自动合并无矛盾断言，矛盾断言在静态编译阶段直接阻断报错。
- **严苛门禁安全边界**：
  - 区分草稿试跑（`TRIAL_SNAPSHOT`）、排障局部快测（`PARTIAL_SCOPE`）与正式全量门禁（`RELEASED + FULL_PLAN`）；任何试跑或局部重跑均禁止产出全局 `READY` 门禁放行信号。
  - 短期 SSH/Sudo 密码仅驻留内存并在终态立即焚毁，绝不落盘。
- **宽松开源许可（零 `GPLv3` 依赖）**：
  - 采用净室自研 Ansible Inventory 解析器，不依赖 `ansible-core` / `ansible-runner`，全部运行时依赖均为 `Apache-2.0 / MIT / BSD`（详见 [`NOTICE`](./NOTICE)）。

> **当前阶段说明（Phase 1）**：控制面（Schema 校验、RFC 8785 摘要、自包含导入导出、Inventory 解析、参数解析、Goss `v0.4.9` 编译、证据合同归一化、状态机与 TUI/CLI）已 100% 生产级实现；远端执行边界 [`TargetExecutorProtocol`](./src/readyspec/execution/protocol.py) 当前默认挂载仿真执行器 [`MockRemoteExecutor`](./src/readyspec/execution/mock_executor.py)（解析真实编译出的 `goss.yaml` 并生成标准 Goss JSON 结构，界面与报告显式标识 `mock_simulator`）。Phase 2 将无缝接入系统 `OpenSSH` + 官方预编译 `Goss v0.4.9` 实机执行器。

---

## 快速安装与启动

环境要求：**Linux / macOS，Python `>= 3.12`**。本机开发直接装包；界面容器只提供 TUI，不是常驻 Run Manager，也不代表六个目标平台已认证。

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install -e ".[dev]"
```

### 界面容器

镜像是 `linux/amd64` 与 `linux/arm64` 的同一标签，推到 `ghcr.io/fleeto/readyspec:0.1.0`。容器里没有 Run Manager。1 号进程是 `tini` 下的 `sleep infinity`，界面用 `exec` 打开。关闭该窗口会退出这次 TUI 及其进程内仿真，不会保住正在跑的检查。同时只能开一个 TUI，第二个会因工作区锁失败。固定用户是 `uid 10001`，不要用 `--user` 换成别的 uid。

本机加载当前架构：

```bash
docker buildx build --platform linux/amd64 --load -t readyspec:0.1.0 .
docker run -d --name readyspec -v readyspec-ws:/workspace readyspec:0.1.0
docker exec -it readyspec readyspec tui
```

两种架构一起发布：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t ghcr.io/fleeto/readyspec:0.1.0 --push .
```

### 1. 启动四幕交互式 TUI

```bash
readyspec tui
```

首次启动会自动初始化演示工作区（包含已发布方案 `order-platform@1.0.0`、草稿方案 `payment-gateway@0.2.0`、历史检查报告及待清理残留演示项）：

- `1` / `F1`：**首页**（双角色任务卡片 + 最近运行记录）
- `2` / `F2`：**方案工作台**（`a` 双栏模板选择器、`c` 自定义 Shell 离线沙盘、`d` 跨角色复制、`t` 快照试跑、`r` 正式发布导出）
- `3` / `F3`：**运行与报告**（四步执行向导、`F5` 立即启动、9 阶段实时看板、按主机/检查项双视角报告、`t` 仅快测异常主机、`r` 全量复测）
- `4` / `F4`（或 `!`）：**模板库与系统**（9 大领域 54 检查组目录检索 + 三重归属指纹受控清理向导）

### 2. 常用 CLI 与自动化门禁命令

```bash
# 查看 9 大领域 54 个检查组完整目录（含 40 项已集成 P0 模板）
readyspec template list --catalog

# 执行前静态离线预览（零远端连接，输出主机数、候选检查数与特权需求）
readyspec run preview \
  --plan order-platform@1.0.0 \
  --inventory "web-01=10.20.1.11,web-02=10.20.1.12,db-01=10.20.2.21" \
  --role-map web=web-01,web-02 --role-map db=db-01

# 混合故障场景执行（末台 DB 节点 /data 空间不足 -> 返回退出码 10: NOT_READY）
readyspec run submit \
  --plan order-platform@1.0.0 \
  --inventory "web-01=10.20.1.11,web-02=10.20.1.12,db-01=10.20.2.21" \
  --role-map web=web-01,web-02 --role-map db=db-01 \
  --approve-privileged A03 --profile mixed_realistic

# 通过受控现场覆盖将 DB 数据盘指向大容量 /data2（返回退出码 0: READY）
readyspec run submit \
  --plan order-platform@1.0.0 \
  --inventory "web-01=10.20.1.11,web-02=10.20.1.12,db-01=10.20.2.21" \
  --role-map web=web-01,web-02 --role-map db=db-01 \
  --approve-privileged A03 \
  --override db.db_data_space.path=/data2 \
  --profile mixed_realistic
```

### CLI 门禁退出码契约

| 退出码 | 含义 | 典型触发场景 |
| :---: | :--- | :--- |
| `0` | `READY`（门禁放行） | 仅限 `RELEASED` 正式方案 + `FULL_PLAN` 全量范围且所有 `REQUIRED` 与 `P01–P04` 聚合通过 |
| `10` | `NOT_READY` | 存在 `REQUIRED` 检查项或拓扑聚合检查 `FAIL` |
| `11` | `UNKNOWN` / 非正式门禁范围 | 存在 `UNKNOWN`、或本次运行属于 `TRIAL_SNAPSHOT` 试跑 / `PARTIAL_SCOPE` 局部排障快测 |
| `12` | 安全策略或权限拒绝 | 未显式批准特权模板、`--approve-root-ssh` 未授权、或检测到伪造特权声明 |
| `13` | 静态校验或编译冲突 | JSON Schema 校验失败、YAML 重复键、Inventory 同端点重复命名、Goss 资源矛盾冲突 |
| `14` | 运行被取消 | 用户主动取消执行（`CANCELLED`） |
| `15` | 控制器并发锁冲突 | 非交互 `--no-wait` 模式下已有活动运行持有工作区排他锁 |

---

## 开发与质量门禁

```bash
ruff check src tests
pyright src tests
pytest -v
```

完整产品边界、数据模型、54 检查组目录与安全基线详见：[`ReadySpec_V1_最终产品与技术规格.md`](./ReadySpec_V1_最终产品与技术规格.md)。

---

## 开源许可

本项目基于 [Apache License 2.0](./LICENSE) 开源发布，第三方组件与规范声明见 [`NOTICE`](./NOTICE)。
