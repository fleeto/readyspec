# M4 AWS 账号权限备用说明

本文汇总 ReadySpec M4 Linux 验收所需的 AWS 身份权限，供后续管理员配置时参考。它不是 AWS 凭据，也不代表权限已附加。具体可执行策略以本目录中的策略 JSON 为准；使用前必须按目标账号、区域、Session 和当前官方 AMI 更新并复核。

## 权限边界

建议通过专用 IAM Role 或专用身份授予两份客户托管策略，不把权限粘贴到用户内联策略，也不授予 `AdministratorAccess`、IAM 策略管理权限或长期凭据。

| 权限组 | 主要用途 | 参考文件 |
|---|---|---|
| 区域盘点与只读发现 | 读取调用者身份、区域/AZ、AMI、实例规格与配额、VPC/子网/路由/安全组、公钥、卷/快照/网卡及 EBS 默认加密；校验 IAM 策略 | [m4-linux-network-read-policy.json](m4-linux-network-read-policy.json) |
| 网络与公钥生命周期 | 创建并配置专用 VPC、子网、路由表、IGW、安全组，导入公钥；仅管理本 Session 创建且带标签的网络资源 | 同上 |
| 计算、存储与镜像 | 在批准规格、加密 gp3 容量和 IMDSv2 条件下启动实例；启动/停止/终止实例；创建、使用和清理本 Session 镜像、快照、卷与网卡 | [m4-linux-compute-policy-v2-exact-amis.json](m4-linux-compute-policy-v2-exact-amis.json) |
| 创建时打标签 | 仅在资源创建 API 上打标签，限制 `Project=readyspec-m4`、当前 `Session` 和允许的标签键 | Compute 策略中的 `TagOnlyDuringResourceCreation` |

两份策略必须同时使用：创建网络资源时也需要 Compute 策略中的 `ec2:CreateTags`。客户托管策略可拆分以符合单份策略大小限制。完整覆盖策略模板见 [m4-full-linux-policy.template.json](m4-full-linux-policy.template.json)，它适用于专用角色策略，包含可选项；不要不经裁剪直接附加。

## 主要限制条件

- 把 `REGION`、`ACCOUNT_ID`、`SESSION_ID` 和公开 AMI ARN 替换为本次验收的实际值。Session 应唯一且短期使用；不可复用历史 Session ID 或照抄旧 AMI 清单。
- 资源需带 `Project=readyspec-m4` 与当前 `Session` 标签；新增资源同时限制区域、允许的标签键和资源类型。清理权限也按这两个标签限定。
- `RunInstances` 限定为按需、默认租用、指定小型实例规格、IMDSv2 必需；EBS 只允许加密 gp3，并限制容量、IOPS 和吞吐量。
- 网卡按用途标记：`private-validation` 必须不分配公网 IP；`public-bootstrap` 仅用于准备阶段。正式私网验收应使用无公网 IP 的目标网卡，并按计划关闭不需要的出站路径。
- 公共镜像应使用管理员核实过的精确 AMI ARN；自制镜像和快照仅允许本账号、当前 Session 的资源。已验证策略使用精确 AMI ARN，因为宽泛 Owner 条件可能无法匹配 EC2 返回的镜像所有者信息。
- Marketplace 镜像/订阅不属于默认授权范围；当前策略通过拒绝带 ProductCode 的镜像来避免无意订阅或产生额外费用。

## 单独处理的权限

- **AWS Budget 与账单**：当前验收策略不包含账单读取、预算查看或修改权限。若需核实预算，由账单管理员单独授予只读账单/预算访问；修改预算、通知阈值或付款设置不属于验收执行角色。AWS 预算告警不是硬性花费上限。
- **IAM 管理**：执行身份不应拥有创建用户、附加策略、创建访问密钥或修改角色信任策略的权限。管理员应在执行前配置并在任务后撤销临时授权。
- **KMS**：策略不授予 KMS 密钥管理权限。EBS 加密应使用账户默认加密或 AWS 管理的 EBS 密钥；如组织强制指定客户管理密钥，应由管理员另行设计最小化授权并先验证。
- **压力与 Windows 验收**：不为已排除的压力测试/性能计量或尚待 Windows 11 VM 的验收扩张当前策略范围。

## 配置和撤销流程

1. 管理员核对目标账号、执行身份、区域、配额和预算告警；为本次任务生成唯一 Session ID。
2. 核实六个目标发行版/架构对应的公开 AMI 来源、架构、区域和最新 AMI ID，更新精确 ARN 列表。
3. 在 IAM 创建两份客户托管策略并附加到专用角色；用 IAM Access Analyzer 校验，再用策略模拟检查允许项和拒绝项，包括错误 Session、越界规格、未加密卷、IMDSv1、Marketplace 镜像及私网网卡申请公网 IP。
4. 先对带有本次 Project/Session 标签的网络创建与公钥导入请求执行 EC2 dry run；再按计划逐步建立资源，并记录实际资源清单。
5. 验收完成后按精确资源 ID 和 Project/Session 双标签回收实例、镜像/快照、卷、网卡、网络资源与公钥；复查残留为零，再撤销临时策略附件。

历史策略与权限模拟记录见 [AWS 权限核查报告](../m4-aws-permission-audit-20261108.md)。历史报告中的账号、Session、AMI 和权限结果仅作为审计证据，不能直接当作下一次任务的授权参数；策略模拟通过也不等同于实际资源创建或验收完成。
