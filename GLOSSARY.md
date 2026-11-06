# ReadySpec

Linux 软件部署前环境要求定义、目标主机检查与就绪结论门禁领域上下文。

## Language

### 方案与模板

**检查模板 (CheckTemplate)**:
对某一类系统环境条件的可参数化检查定义。
_Avoid_: 检查组, CatalogGroup, 规则原型, 检查能力

**检查方案 (Plan)**:
面向特定待部署软件版本的完整环境要求定义文件（生命周期状态为 `DRAFT`、`RELEASED`、`ARCHIVED`）。
_Avoid_: 检查计划, VALIDATED, TRIAL_SNAPSHOT, 试跑快照方案, 移植导出包

**节点角色 (Role)**:
待部署软件在部署拓扑中的逻辑节点分工（如 `web`、`db`、`cache`）。
_Avoid_: 业务角色, 主机类型, HostType

**检查项 (CheckItem)**:
某个节点角色下声明的一条具体环境要求。
_Avoid_: 检查实例, CheckInstance, 规则实例

**现场可调参数 (OverridableParam)**:
检查项中声明允许部署人员在发起检查时按现场环境调整的参数。
_Avoid_: 运行时可覆盖开关, 受控覆盖参数

**环境与拓扑要求 (TopologyPolicy)**:
检查方案声明的目标 CPU 架构范围及各节点角色的机器数量与混部条件。操作系统发行版与版本要求通过具体检查项表达。
_Avoid_: 控制端拓扑聚合检查, P01–P04 模板, 兼容矩阵检查项

### 目标与执行

**主机清单 (Inventory)**:
单次检查针对的目标服务器连接地址、登录身份与分组列表。
_Avoid_: 资产库, 主机台账, CMDB

**目标主机 (TargetHost)**:
主机清单中待检查的一台 Linux 服务器。
_Avoid_: 受管节点, 被控端, 客户端主机

**执行配置 (RunConfig)**:
发起一次检查任务时提交的检查方案引用、主机清单、节点角色分配与现场参数值等输入的组合。
_Avoid_: 运行绑定, RunBinding, 运行意图快照, RunIntentSnapshot

**现场参数值 (FieldOverride)**:
执行配置中针对某台目标主机或主机组的现场可调参数填入的实际取值。
_Avoid_: 运行时参数覆盖规则, ParameterOverrideRule

**检查任务 (Run)**:
按一份执行配置对目标主机发起的一次环境检查。
_Avoid_: 巡检作业, TRIAL_SNAPSHOT 运行

**正式门禁 (GateRun)**:
作为部署放行依据的正式检查任务类别。
_Avoid_: 生产运行, 门禁模式

**草稿试跑 (TrialRun)**:
基于草稿方案发起的调试验证检查任务类别。
_Avoid_: TRIAL_SNAPSHOT, 试跑快照

**全量检查 (FullRun)**:
覆盖执行配置中全部绑定节点角色与目标主机的检查范围。
_Avoid_: FULL_PLAN

**局部复测 (PartialRun)**:
仅针对执行配置中选定子集目标主机的排障复查范围。
_Avoid_: PARTIAL_SCOPE, 局部排障范围

### 结果与取证

**单项结果 (ItemResult)**:
单条检查项在目标主机上的判定结论（`PASS`、`FAIL`、`ERROR`、`UNSUPPORTED`、`SKIP`）。
_Avoid_: CheckStatus, 规则状态

**就绪结论 (Verdict)**:
整次检查任务对待部署软件给出的总体门禁判定（`READY`、`NOT_READY`、`UNKNOWN`）。
_Avoid_: ReadinessStatus, gate_eligible, 准备度

**取证方式 (EvidenceMode)**:
采集检查事实的观测类别（`EXISTENCE`、`METADATA`、`CONFIG`、`EFFECTIVE_STATE`、`STATIC_BINARY`、`ACTIVE_PROBE`、`LOG_HISTORY`）。
_Avoid_: 最低证据合同, EvidenceContract, 证据深度, EvidenceDepth, L0–L3

**检查报告 (RunReport)**:
一次检查任务结束时生成的权威结果与证据归档。
_Avoid_: 巡检报表, 运行快照

### 权限与清理

**普通执行 (STANDARD)**:
直接使用当前登录会话身份、不经过提权通道的检查执行方式。
_Avoid_: 标准权限组

**特权执行 (PRIVILEGED)**:
通过受控提权通道以系统管理员权限运行的检查执行方式。
_Avoid_: 特权权限组

**远端残留清理 (RemoteCleanup)**:
针对异常中断遗留的远端临时工作目录与疑似关联进程的归属核验及清理领域。
_Avoid_: 系统恢复, Recovery, 三重归属指纹清理

**待清理残留 (CleanupItem)**:
尚未确认已在目标主机清理完毕的一条远端临时目录与关联进程记录（`CLEAN`、`PENDING`、`FAILED`、`UNKNOWN`、`IGNORED`）。
_Avoid_: CleanupRecord, 恢复待办, WAIVED
