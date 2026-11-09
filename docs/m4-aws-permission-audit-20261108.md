# M4 AWS 当前身份权限核查（2026-11-08）

核查身份为 `arn:aws:iam::891376991548:user/lightsail-hk-readonly-20260906`，区域为 `us-east-1`，新 Session 为 `117f28ab8bdbd660bf43405d`，旧 Session 为 `d6b321143e61d7bf2a6c6b5f`。本次只进行策略模拟及策略校验，没有创建或修改 AWS 资源、附加策略或执行产品测试。

## 补权后复核

用户确认已添加权限后，重新使用当前身份的实际附加策略执行96组模拟，没有传入任何额外 `PolicyInputList`。新 Session 的67个任务操作全部获允许，包括基础计算、实例回收和镜像/快照创建、使用与删除。六个公开AMI、五种批准规格，以及公网准备/私网验收启动授权均通过。`budgets:ViewBudget` 仍为隐式拒绝。

随后对携带本次 Project/Session 标签的 `CreateVpc` 和 `ImportKeyPair` 执行实际EC2 API dry run，两者均返回 `DryRunOperation`，确认权限检查通过；没有创建资源。复核原始模拟证据位于 `/tmp/opencode/readyspec-permission-audit-g0aci6h7/simulation.json`，dry run证据位于同目录的 `dry-run.json`。下文保留补权前结果与策略设计依据，历史缺口已在本次复核范围内关闭；实际实例启动与验收仍需后续执行。

## 补权前结果

对照实际网络/启动/清理脚本及完整 Linux 策略，共核查 67 个不同的任务操作。当前条件下，53 个获允许、14 个被隐式拒绝；另外查询的 `budgets:ViewBudget` 也被隐式拒绝。不能将隐式拒绝理解为显式 Deny。

96 组初始模拟包括新旧 Session 对照、六个批准的公开 AMI、五种批准规格，以及公网准备/私网验收网卡。只改变 Session，即可将相关计算操作由允许变为拒绝，说明既有授权仍绑定旧 Session。

| 操作类别 | 新 Session 结果 |
|---|---|
| 29 个身份/定价/EC2 元数据/配额/策略校验操作 | 允许 |
| VPC、子网、路由表、IGW、安全组的建立、管理和删除 | 允许 |
| `ec2:CreateTags`、`ec2:DeleteNetworkInterface` | 允许 |
| 六个批准公开 AMI 的 `ec2:RunInstances` 镜像资源授权 | 允许；不代表完整启动请求通过 |
| `ec2:ImportKeyPair`、`ec2:DeleteKeyPair` | 拒绝 |
| `ec2:RunInstances` 的新实例、卷、网卡、子网、安全组、公钥及新 Session 自有 AMI/快照授权 | 拒绝 |
| `ec2:StartInstances`、`ec2:StopInstances`、`ec2:RebootInstances`、`ec2:TerminateInstances` | 拒绝 |
| `ec2:GetConsoleOutput`、`ec2:DescribeInstanceAttribute` | 拒绝 |
| `ec2:DeleteVolume` | 拒绝 |
| `ec2:CreateImage`、`ec2:CreateSnapshot`、`ec2:DeregisterImage`、`ec2:DeleteSnapshot` | 拒绝 |

模拟匹配的策略来源包括：网络和创建标签来自 `AmazonVPCFullAccess`；旧 Session 的公钥来自 `m4-linux-network-read-policy`；旧 Session 的实例管理和启动来自 `m4-linux-compute-policy`。当前无法枚举策略原文，但上述来源由模拟 API 的 `MatchedStatements` 返回，不是根据本地文件名推测。

## 管理员补充授权

创建客户托管策略并附加到上述用户；不要粘贴到用户内联策略入口。

| 文件 | 去空白字符数 | 用途 |
|---|---:|---|
| [基础计算补充](aws/m4-linux-current-session-core.json) | 3,272 | 补齐公钥、公开 AMI 实例启动、实例管理/诊断与磁盘清理；基本启动需要此项 |
| [镜像恢复补充](aws/m4-linux-current-session-images.json) | 1,573 | 补齐原计划中夹具 AMI 制作、自有 AMI/快照恢复和回收；保留原镜像流程时附加 |

两份均低于客户托管策略的 6,144 字符上限。基础计算补充依赖当前已获允许的六个公开 AMI 授权和创建标签权限；它不是替代所有现有策略的独立策略。没有添加 IAM 管理、Marketplace 订阅、KMS 管理或预算修改权限。

AWS Access Analyzer 对两份策略均返回 `findings: []`。将两份策略作为 `SimulatePrincipalPolicy` 的 `PolicyInputList` 临时加入模拟，不实际附加，69 组核验全部符合预期：原拒绝操作获允许，六个公开 AMI 的完整多资源启动授权在仅补充基础计算策略时均获允许；错误项目/Session、超规格实例、IMDSv1、Spot、未加密/超限磁盘及私网网卡带公网 IP 的计算资源负例仍被拒绝。

## 证据与适用边界

- 初始原始请求和响应：`/tmp/opencode/readyspec-permission-audit-kfaahbhv/simulation.json`。
- 补充策略及负例原始请求和响应：`/tmp/opencode/readyspec-permission-audit-kfaahbhv/supplement-simulation.json`。
- 模拟使用合成资源 ARN，显式提供项目、Session、区域、实例、磁盘和网卡条件。公开 AMI 不带 Marketplace 产品代码，因此 `ec2:ProductCode` 刻意不传；部分拒绝结果将它列为缺少上下文。为排除混淆，又给新旧 Session 公钥操作补传产品代码作为对照，仍然新 Session 拒绝、旧 Session 允许。
- 策略模拟不创建资源，不验证容量、配额、AMI 引导、加密磁盘的实际服务端访问，也不能完整证明所有 SCP 条件与资源策略均可通过。补权后仍需使用实际资源参数进行 dry run 和逐步建立/回收验证；不将模拟通过当作 Linux 产品验收通过。
- 当前 `AmazonVPCFullAccess` 的网络及标签授权没有 Session 隔离，补充策略不会收窄已有 Allow。负例通过只针对上述计算资源条件，不代表网络资源已受同样限制。
- `budgets:ViewBudget` 拒绝不阻止 EC2 操作，但当前身份不能核实 AWS Budget。30 美元仍为执行成本上限，不是 AWS 自动断电限制；预算和告警可由管理员维护。
- 附加后的有效权限已复测，见本文“补权后复核”。临时审计权限可以在不再需要权限诊断时移除。
