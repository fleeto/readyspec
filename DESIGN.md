# DESIGN.md — DevOps Control Plane Design System

> Version: 1.0 · License: CC0-1.0 (public domain) · Intended for OpenCode / Codex / other coding agents
>
> Purpose: Build a serious, fast, information-dense DevOps console for infrastructure, deployments, observability, and operations. This file describes UI/UX rules, not application architecture.

> **ReadySpec 适用与排除章节声明（项目绑定说明）**：
> - **适用范围**：本文件的产品气质（§1）、设计原则（§2）、深浅色语义令牌（§3）、字体与等宽数字排版（§4）、间距与几何（§5）、应用壳层结构（§6 `App shell`：左侧折叠栏 + 顶栏 + 主工作区 + 右侧上下文抽屉）、基础组件规范（§7，不含持续监控时序曲线 `Charts`）、交互动效（§9）、响应式断点（§10）、无障碍与国际化（§11）、线性图标规范（§12）、编码代理指引（§13）与验收清单（§14）作为 ReadySpec Web UI 的**唯一视觉与组件令牌来源**。
> - **明确排除范围**：ReadySpec 不是 CMDB 主机资产库、持续监控平台、CI 流水线编排器或事故响应系统（见 [`SPEC.md`](./SPEC.md) §2.4）。因此，本文件 **§6 的通用主导航示例**（`Infrastructure` / `Delivery` / `Observability` 等）、**§7 `Charts` 时序监控折线图** 以及 **§8 的四张通用样板屏幕**（Fleet 趋势仪表盘、常驻主机资产列表、CI/CD 流水线阶段图、事故详情页）**不适用于 ReadySpec**；ReadySpec 的实际主导航（`总览`、`方案工作台`、`检查与报告`、`模板与清理`）与屏幕业务结构以 [`SPEC.md`](./SPEC.md) §10.1 及 [`GLOSSARY.md`](./GLOSSARY.md) 为准。

> **App shell 领域映射**：采用侧栏、顶栏、主工作区和上下文抽屉的结构；单一工作区只显示当前工作区标识，不提供工作区选择器。没有独立环境实体、用户管理或持续监控数据时，不显示环境切换、用户/profile、全局时间范围、通知等通用控件；环境要求通过方案的 `TopologyPolicy` 表达，任务时间通过具体任务记录表达。仅展示已接入的能力；未实现的执行器和模板不得显示为可执行或已通过检查。

## 1. Product character

**Keywords:** operational, precise, trustworthy, technical, calm, dense, auditable.

Use the visual language of a modern infrastructure control plane: neutral dark surfaces, restrained cyan/blue accents, clear status semantics, excellent numeric legibility, compact navigation and data tables, and visible relationships between services, machines and deployments.

**Do:** prioritize scanning, comparisons, incident triage, long-running operations, keyboard navigation, and safe destructive actions.

**Don't:** make the UI look like a gaming dashboard, neon cyberpunk scene, marketing landing page, glassmorphism showcase, or colorful card wall. Avoid gratuitous gradients, excessive rounded corners, giant empty spaces, emoji as status indicators, and motion that distracts from operational state.

## 2. Design principles

1. **State first.** Show health, freshness, ownership, environment, and last change before decorative context.
2. **Information density with hierarchy.** Display more useful rows and signals without sacrificing readability.
3. **Reliable semantics.** Never convey status solely by color; pair it with an icon, text and/or shape.
4. **Safe operations.** Make production scope clear; require confirmation for destructive or broad-impact actions.
5. **Traceable changes.** Include timestamps, actor, environment, version/commit and related logs when useful.
6. **Progressive disclosure.** Show essentials initially; details in drawers, tabs or drilldowns.
7. **Fast interaction.** Prefer keyboard-friendly controls, persistent filters and predictable placement.

## 3. Color system

Default theme is **dark**. Implement an equally usable light theme with semantic tokens rather than hardcoded component colors.

### Dark theme tokens

| Token | Hex | Use |
|---|---|---|
| `--bg-canvas` | `#0B1017` | App background |
| `--bg-sidebar` | `#0E1620` | Navigation |
| `--bg-surface` | `#131D28` | Panels/cards |
| `--bg-elevated` | `#1A2734` | Popovers/dialogs/hover |
| `--bg-input` | `#101923` | Inputs/code controls |
| `--border-subtle` | `#233140` | Dividers |
| `--border-strong` | `#344759` | Focusable control outlines |
| `--text-primary` | `#E9F0F6` | Main content |
| `--text-secondary` | `#A8BAC9` | Supporting text |
| `--text-muted` | `#8195A7` | Metadata |
| `--accent` | `#4DB6D6` | Primary actions/active nav |
| `--accent-hover` | `#73CAE3` | Hover |
| `--accent-subtle` | `#123442` | Accent background |
| `--success` | `#43C59E` | Healthy/succeeded |
| `--warning` | `#F1B85B` | Degraded/pending |
| `--danger` | `#F17979` | Failed/critical/destructive |
| `--info` | `#75A7F2` | Informational/running |
| `--neutral` | `#93A5B7` | Unknown/disabled |

### Light theme tokens

| Token | Hex |
|---|---|
| `--bg-canvas` | `#F4F7FA` |
| `--bg-sidebar` | `#EAF0F5` |
| `--bg-surface` | `#FFFFFF` |
| `--bg-elevated` | `#FFFFFF` |
| `--bg-input` | `#F8FAFC` |
| `--border-subtle` | `#DCE5ED` |
| `--border-strong` | `#A9BAC9` |
| `--text-primary` | `#162634` |
| `--text-secondary` | `#43596A` |
| `--text-muted` | `#5D7283` |
| `--accent` | `#126C91` |
| `--accent-hover` | `#0D5879` |
| `--accent-subtle` | `#E0F2F8` |
| `--success` | `#13795B` |
| `--warning` | `#986000` |
| `--danger` | `#B72F3D` |
| `--info` | `#275FAF` |
| `--neutral` | `#637789` |

**Rule:** Semantic status colors have fixed meanings; do not repurpose red for a normal brand accent. Validate actual text/background pairs against WCAG AA (4.5:1 normal text, 3:1 large text and non-text UI boundaries where required). Do not assume token values guarantee accessible contrast in every combination.

## 4. Typography

- UI sans: `Inter`, fallback `system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`.
- Monospace: `JetBrains Mono`, fallback `SFMono-Regular, Consolas, ui-monospace, monospace`.
- Use **tabular numerals** for metrics, durations, counts and timestamps.
- Sizes: page title 24/32 semibold; section title 16/24 semibold; body 14/20; dense table 13/20; metadata 12/18; prominent KPI 28/36 semibold.
- Avoid all-caps long labels. Use uppercase only for short technical tags such as `PROD`, `P95`, `CPU`.
- Preserve code, identifiers and log text without stylistic text transformation.

## 5. Spacing and geometry

- Spacing scale: `4, 8, 12, 16, 20, 24, 32, 40` px.
- Primary content padding: 24px desktop; 16px tablet/mobile.
- Sidebar: 240px expanded, 64px collapsed.
- Top bar: 56px; compact table row: 40–44px; form control: 36–40px.
- Card radius: 8px; input/button radius: 6px; badge radius: 4px. Avoid fully pill-shaped buttons except small filter chips.
- Borders: 1px subtle; light shadows only on elevated overlays. Prefer borders to heavy shadows.
- Layout: responsive 12-column desktop grid; 8px base gap; primary working area should expand to use available width.

## 6. Information architecture and navigation

Recommended primary navigation:

- **Overview** — estate health, incidents, deployments, capacity.
- **Infrastructure** — hosts, clusters, nodes, namespaces, workloads.
- **Delivery** — pipelines, releases, deployments, environments.
- **Observability** — metrics, logs, traces, alerts.
- **Operations** — jobs, runbooks, change history, audit logs.
- **Settings** — connections, access, integrations, preferences.

App shell:

1. Left sidebar: product icon/name, workspace selector, nav groups, settings/profile.
2. Top bar: breadcrumb, global search/command palette, time range, environment switcher, notifications.
3. Main content: page heading + brief context, action row, filters, core content.
4. Optional right drawer: contextual details; do not replace primary task content with unnecessary modal dialogs.

Always make **environment scope** visible (e.g., `Production`, `Staging`, `Development`). Use prominent warnings for bulk/production-affecting actions, without painting the whole application red.

## 7. Component rules

### Buttons

- Primary: accent filled; only one dominant primary action per local region.
- Secondary: neutral surface and border.
- Tertiary: text/icon for low-priority actions.
- Destructive: danger-colored with explicit verb (`Delete cluster`), never ambiguous `Confirm`.
- Support loading, disabled, tooltip, keyboard focus, and permission-denied states.

### Status labels

Use icon + text + semantic color:

| State | Label examples | Color |
|---|---|---|
| Healthy | `Healthy`, `Ready`, `Succeeded` | Success |
| Running | `Running`, `Deploying` | Info |
| Pending | `Queued`, `Pending` | Warning/neutral |
| Degraded | `Degraded`, `Warning` | Warning |
| Failed | `Failed`, `Critical` | Danger |
| Unknown | `Unknown`, `No data` | Neutral |

Distinguish **last known healthy** from **currently healthy**. Display freshness (`Updated 35s ago`) and stale-data warnings.

### ReadySpec status mapping

This domain mapping takes precedence over the generic examples above. Use the
literal code with the Chinese label wherever disambiguation is needed. A completed
task does not itself mean that the deployment environment is ready.

| Domain | Code and label | Token | Icon |
|---|---|---|---|
| Plan | `DRAFT` 草稿 | neutral | Circle |
| Plan | `RELEASED` 已发布 | success | CheckCircle2 |
| Plan | `ARCHIVED` 已停用 | neutral | MinusCircle |
| ItemResult | `PASS` 通过 | success | CheckCircle2 |
| ItemResult | `FAIL` 不满足 | danger | XCircle |
| ItemResult | `ERROR` 取证错误 | warning | AlertCircle |
| ItemResult | `UNSUPPORTED` 不支持 | neutral | MinusCircle |
| ItemResult | `SKIP` 已跳过 | neutral | Circle |
| Verdict | `READY` 就绪 | success | CheckCircle2 |
| Verdict | `NOT_READY` 未就绪 | danger | XCircle |
| Verdict | `UNKNOWN` 无法确定 | neutral | Info |
| ExecutionMode | `STANDARD` 普通执行 | neutral | Circle |
| ExecutionMode | `PRIVILEGED` 特权执行 | warning | ShieldAlert |

Do not reuse `PASS` for static plan validation: use “静态校验通过” and state that
target readiness has not been established. Catalog entries without trusted
implementations must say “规格定义” and provide no execution action. An empty
cleanup list must not claim that remote hosts were checked.

### Tables

- Prefer a table for hosts, pipelines, alerts, deployments and logs.
- Sticky header when scrolling; explicit sorting state; selectable rows; bulk action toolbar.
- Keep primary identifier left, actions right; right-align numeric columns.
- Show zero-state, loading skeleton, partial error, pagination/virtualization as appropriate.
- Make filters shareable in URL when reasonable, and provide a `Reset filters` action.
- Use subtle zebra striping only if needed; avoid strong row background fills.

### Metric cards

- Label + primary value + unit + change/threshold + timeframe.
- Avoid meaningless positive/negative coloring: CPU increase isn't inherently good; interpret against thresholds.
- Sparklines are supporting evidence, not substitutes for numeric values.
- State units explicitly (e.g., `ms`, `%`, `GiB`, `req/s`).

### Charts

- Prefer time-series line charts for CPU, memory, latency, error rate, throughput and queue depth.
- One unit per axis; consistent colors per series and semantic thresholds.
- Show time zone, selected range, missing data, and legends; mark incidents and deploy events.
- Use p50/p95/p99 labels for latency; never invent sample precision.
- Never imply gaps in telemetry are zero values.

### Logs, terminal and code

- Monospace 12–13px; dark code background distinct from panels.
- Preserve whitespace; optionally show line numbers and timestamps.
- Severity labels (`DEBUG`, `INFO`, `WARN`, `ERROR`) and copy/download controls.
- Offer wrapping toggle, follow-tail toggle and pause, search and filtering.
- Redact secrets and avoid exposing sensitive tokens in copied diagnostics.

### Forms and dialogs

- Place persistent labels above fields; helper text below.
- Inline validation adjacent to invalid field; preserve entered values on errors.
- Clearly separate connectivity test from save/commit operations.
- For high-impact actions show object, environment, blast radius, and an explicit confirmation step.

## 8. Canonical screens

### Overview dashboard

Top: page title, workspace/environment, last refresh, time range.

Row A: high-signal KPIs — healthy services, open incidents, active deployments, error rate.

Row B: fleet resource trends (CPU/memory/network) and deployment activity timeline.

Row C: actionable alerts table and recent operational events.

**Do not** fill the page with generic pie charts or decorative gauges.

### Host / cluster list

Toolbar: query search, environment, region, status, owner, label filter, refresh, add/connect.

Columns: name, status, environment, role, version, CPU, memory, region, last seen, actions.

Detail: resource trends, processes/workloads, events, agent health, connectivity, audit history.

### CI/CD pipeline details

Show commit SHA, branch, actor, target environment, pipeline duration, start time, overall outcome.

Render stages as a readable sequential graph with state, duration and retry affordance. Selecting a stage opens its logs and artifacts. Highlight manual gates and production approvals.

### Incident / alert details

Summary, severity, affected services, started/last observed time, ownership, timeline, correlated deployments, relevant charts/logs, acknowledgment and resolution actions. Separate observed facts from hypotheses.

## 9. Interaction and motion

- Hover states should subtly shift surface/border, not bounce.
- Animation durations: 120–180ms for menus and affordances; 180–240ms for drawers.
- Respect `prefers-reduced-motion`.
- Long operations: show progress, current phase, durable job ID and retrievable history when applicable.
- Use toasts for noncritical success; keep errors discoverable and actionable.
- Destructive actions should not be triggered from accidental single clicks.
- Support `/` or `Cmd/Ctrl+K` for global search when the platform allows; preserve expected OS/browser shortcuts.

## 10. Responsive behavior

- **≥ 1280px:** expanded left sidebar, multi-column metrics/charts, wide tables.
- **768–1279px:** collapsible sidebar, narrower panels; retain filters behind compact controls.
- **< 768px:** stacked panels, drawer navigation, deliberate horizontal scrolling for dense tables; do not hide critical status or action outcomes.
- Avoid collapsing operational tables into unreadable multi-line cards by default. Prioritize key columns and make additional fields accessible.

## 11. Accessibility and internationalization

- WCAG 2.2 AA target; visible focus, accessible names, meaningful landmarks, proper table semantics.
- All actions reachable by keyboard; escape closes popovers/dialogs with focus restored appropriately.
- Never use color alone to communicate alert severity or deployment state.
- Support English and Chinese labels without layout breakage; allow longer translated text.
- Use explicit timezone for timestamps and unambiguous date formats such as `2026-11-04 16:28 UTC+08:00` where useful.
- Support number grouping and units consistently.

## 12. Icons and visual assets

- Prefer Lucide or a comparable coherent outline icon set (16–20px strokes).
- Use familiar symbols for server, network, database, terminal, activity, git branch, shield, alert and settings.
- Do not introduce decorative illustrations into routine operational screens.
- No emoji as primary UI icons.

## 13. Implementation guidance for coding agents

When generating UI from this document:

1. Inspect the existing repository and **reuse its current framework, component library and styling system**. Do not add a new dependency just for appearance unless necessary.
2. Implement theme tokens centrally. Never scatter arbitrary hex values throughout components.
3. Use authentic-looking *clearly labeled demo data* if backend data is unavailable. Never portray dummy operational status as live.
4. Implement complete states: loading, empty, filtered-empty, error, stale, permission-denied and success.
5. Prioritize real screen workflows: filter → inspect → act → verify, rather than static mockups.
6. Maintain a consistent status vocabulary and icon/color mapping across pages.
7. Make charts and tables readable on laptop screens and at browser zoom.
8. Include tests where the project supports them, especially for destructive confirmations, filter behavior and status state mapping.
9. Do not rewrite or re-architect unrelated code while applying visual design.
10. If this file conflicts with existing product design requirements, ask for resolution or document the chosen tradeoff.

## 14. Acceptance checklist

- [ ] Feels like a professional DevOps control plane, not a marketing site.
- [ ] Environment and data freshness are visible wherever operationally relevant.
- [ ] Major status meanings are consistent across screens.
- [ ] Tables support realistic search/filter/sort and readable numeric data.
- [ ] Log and code surfaces use monospace and preserve content fidelity.
- [ ] Production/destructive actions include clear impact and confirmation.
- [ ] Loading, empty, failure, stale and permission states are represented.
- [ ] Dark and light themes maintain usable contrast.
- [ ] Keyboard focus and essential interactions are accessible.
- [ ] Responsive layout supports laptop and narrow screens.
- [ ] No gratuitous neon effects, gradients, animations or glassmorphism.

---

**Licensing note:** This text is newly authored for this project and may be used, modified and redistributed without restriction (CC0-1.0 intent). Typeface and icon packages mentioned here retain their respective upstream licenses.
