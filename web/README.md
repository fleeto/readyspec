# ReadySpec Web UI

React + TypeScript + Vite + Tailwind CSS + Lucide。视觉令牌统一定义在 `src/styles.css`，业务词汇遵循根目录 `GLOSSARY.md`。

```bash
# 在仓库根目录安装、测试和构建
make web-install
make web-test
make build
```

构建输出到 `internal/webui/dist/`，由 Go 编译期内嵌；构建时同时核验并收集实际包含在产物中的前端依赖许可，写入 `third_party/frontend.LICENSE`。产物运行不使用 CDN、外部字体或 Node.js。

开发时先启动 Go API，并显式允许开发服务器的 Host：

```bash
go run ./cmd/readyspec serve --no-browser \
  --workspace ./.readyspec_workspace \
  --allowed-host 127.0.0.1:5173
```

先通过终端里的完整链接登录一次，再运行 `npm --prefix web run dev` 并打开 `http://127.0.0.1:5173`。同一浏览器共享会话；开发代理保留原始 Host 和 Origin，不绕过生产服务的校验。生产使用不需要 `--allowed-host`；只有通过域名或开发代理访问时才应显式配置对应 `host:port`。

```bash
# 浏览器测试使用独立临时工作区，不连接目标主机
cd web
npx playwright install chromium
npm run test:e2e

# 源码格式检查
npm run format:check
```

浏览器测试覆盖登录、Shell/参数编排、静态校验、未完成草稿的保存与重开、导出与严格导入、主题、移动端筛选及关闭导航的键盘焦点，并覆盖添加内置 S01 模板、配置参数、静态编译、保存与导出。测试截图和导出文件位于 `web/test-results/`，不纳入 Git。Go HTTP 测试覆盖会话恢复、一次性启动令牌、CSRF、Origin/Host、请求限制、模板目录、静态编译和持久化；校验/保存/编译绝不执行用户 Shell。

`GET /api/v1/templates` 返回可用模板快照 `items` 和完整目录 `definitions`，前端不维护另一份模板执行目录。`POST /api/v1/plans/compile` 接受与严格校验相同的 `source` / `normalize_draft`；默认按角色分别预检，返回 `roles[].artifacts` 中的 `standard` 与 `privileged` 独立工件。可选 `host` 提供明确的同机角色和组/主机覆盖，此时只合并所绑定角色并返回主机的两份工件。静态编译成功不表示环境就绪。

“检查与报告”的静态预览使用当前工作台方案，支持快速主机表、Ansible INI/YAML、各角色的主机标识绑定及 JSON 现场参数数组。`POST /api/v1/runs/preview` 接受 `source`、`normalize_draft`、`inventory: {source, format}`、`role_map` 和 `field_overrides`，返回主机工件、候选检查项/特权/Shell/主动探测数量、配置为 root 的主机及身份未验证列表。修改输入后清除旧预览。此流程不收集凭据、不连接目标主机。

草稿保存接受可安全编辑但尚未通过业务校验的 `DRAFT`，请求可携带 `draft_key` 更新同一记录；响应 `validated: false` 且不返回有效摘要。保存不计算或沿用旧摘要，清空暂缺的名称和暂未满足参数约束不会丢失工作区进度。严格校验/导出与严格导入使用 `/plans/validate`：前者可显式携带 `normalize_draft: true`，重新计算模板锁、引用摘要和方案摘要，并应用参数类型、范围、枚举、成员与三态赋值约束；导入使用 `false`，保留并核验原摘要。已发布/已停用文档按原摘要校验且只读，不会被转换成草稿覆盖保存。

会话过期时，前端保留当前编辑器与未保存内容，支持输入访问令牌重新认证，或恢复另一标签页已经建立的会话；操作成功后须重新发起校验/保存。
