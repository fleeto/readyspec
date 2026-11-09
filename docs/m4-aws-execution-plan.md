# M4 AWS 非压力执行与权限计划

**历史窄范围版本：已被 [完整 Linux 资源计划](m4-aws-full-linux-plan.md) 替代。** 下文的管理员预置网络/镜像、40GiB及一律私网启动只描述旧版授权，不用于本轮从空环境准备资源。旧版评审结论保留供追溯，当前请使用完整计划及其权限 JSON。

状态：**评审通过**；Pi + `magpie / kimi-code-cn/k3.1-pro` 两轮评审，首轮 P1-01 已关闭，第二轮无新增设计阻断，主设计者核对后接受。记录见 [AWS 计划评审](m4-aws-review-k3.1-pro.md)。本计划补充 [M4 验收矩阵](m4-acceptance-plan.md)，保留其全部非压力判据；不执行 §7 的负载、压力注入、性能采样，也不签署 F14/TODO-04。当前只制定和评审计划、写入权限 JSON，尚未部署 IAM 或启动资源。

## 1. 环境方案与边界

| 角色 | AWS 或替代来源 | 验证边界 |
|---|---|---|
| B01 | 有浏览器和网络命名空间能力的 Linux 构建机或 GitHub Actions | Node 24；Go/Web/race/隔离采集器/Playwright。既有 d370b54 CI 不代替当前未提交修复的回归 |
| M01 | x86_64 EC2，候选 t3.medium | 原生 linux/amd64 管理端；全新工作区、CLI/Web/锁/SSH |
| M02 | Graviton EC2，候选 t4g.medium | 原生 linux/arm64 管理端；不得以 QEMU 代替 |
| T01–T06 | Ubuntu 24.04、Debian 13、Rocky Linux 9 各 x86_64/arm64 的官方 AMI；候选 t3.small/t4g.small | 专用 VM、真实内核/systemd/SSH、独立机器身份，BASE/MIN/FULL/FAULT 变体按原矩阵记录 |
| M03 | 用户提供的原生 Windows 11 amd64 测试机；或管理员单独评估 AWS Windows 11 Enterprise BYOL/专用硬件 | Windows Server 2025 CI 可补充自动回归，不关闭 Windows 11 DACL/第二账号/锁/真实 Named Pipe Agent/浏览器/SSH 条目 |

Ubuntu/Debian/Rocky 优先使用发行方发布者 AMI，逐区域核对 publisher owner ID、架构、系统版本、root device、产品代码及订阅要求，固定最终 AMI ID，不按镜像名自动信任第三方收费镜像。管理员预先准备与批准变体 AMI/快照；执行角色不具备 CreateImage/CreateSnapshot/镜像注册权限。恢复使用已批准不可变 AMI 重新建机，保留旧资源与替代资源的 EnvID、image/snapshot ID、身份变化及恢复记录。SSH 凭据和主机公钥重新核验，不复用旧 boot-id。

Linux 验收最多同时使用两管理端、六目标和一辅助服务机（共9台），资源不足时分批，压力所需20台目标完全不创建。辅助服务可由管理员预置，若另建 S01 亦使用已批准的六个 AMI 之一。单批按 `--count 1` 创建并等待入账后才创建下一台。t3/t4g 为非压力功能验收候选，不以其 CPU credit 特性做性能结论。

## 2. 前置输入与分阶段执行

R0（只读盘点）：管理员先提供 AWS profile/短期角色、单一区域、预期 account ID；经指定角色调用 `sts get-caller-identity` 与清单中的 Describe API，核对 IAM 实际边界、SCP/权限边界、可用区、x86/arm 配额、AMI、已有 VPC/subnet/SG/keypair、连接路径。没有结果不写虚构资源 ID。当前未读取 AWS 身份配置或调用 AWS API。

R1（管理员准备）：管理员提供专用 VPC 内的固定私有 subnet、固定安全组、已导入的测试公钥、批准的官方/变体 AMI、aws/ebs 加密配置、镜像恢复材料与独立清理负责人。网络仅允许可信管理入口至 M01/M02、管理端至目标 SSH 和测试服务端口；无 `0.0.0.0/0` 或 `::/0` SSH/RDP。执行入口需经已有 VPN/受限堡垒机进入私网；不靠新建 EIP、NAT、IGW、SSM 或实例角色完成引导。依赖预置后封禁目标公网出站，确认 DNS/NTP/辅助服务仍可用。SG/VPC/路由和 Windows BYOL 基础设施均由管理员处理，JSON 不授予这些修改能力。

R2（启动前冻结）：填写 account/region/subnet/SG/keypair、全部允许的 AMI ID 与单次唯一 SESSION_ID；管理员核查每小时估价（实例、EBS、流量、VPN/堡垒等）、最大总预算、最长运行时间、清理期限、负责人，记录具体数值后批准启动。没有数值或许可则 BLOCKED。IAM 不提供实例总数/总费用硬上限，`ExpiresAt` 不是自动删除机制，Budget alert 也不是硬性断电；每次创建前查询本 session 存量与累计费用估算，临近预算立即停止新建并清理。凭据失效不终止实例，必须有独立管理员兜底清理权限。

启动必须从 [单实例请求模板](aws/m4-launch-request.template.json) 渲染并冻结摘要。仅新建一个 `DeviceIndex=0` 主 ENI，显式 `AssociatePublicIpAddress=false`，固定批准 subnet 和唯一 SG，禁止 `NetworkInterfaceId` 引用已有 ENI，禁止额外 ENI/launch template/实例 profile/Spot 参数。该模板只适用于经核查仅含单个 root EBS 的 AMI，`ROOT_DEVICE_NAME` 必须来自 DescribeImages；根卷最小容量须≤40GiB，否则 BLOCKED，不静默扩大权限。所有 instance/volume/ENI 均在创建时打标签。`INSTANCE_TYPE` 按 AMI 架构选择允许的 t3/t4g，EnvID/variant/attempt 合成的 ClientToken≤64字符；完全相同请求重试复用 token，改变输入/重建须新 attempt。操作人和管理员在任何 dry-run/启动前将解析后 JSON 与本模板结构逐字段比对，遇未知字段或已有 ENI ID 则拦截；保存渲染输入及摘要。只用 `--cli-input-json file://...` 加指定 profile/region（预检另加 `--dry-run`），不得追加覆盖启动参数。IAM 的公网 IP Bool 条件是强制约束；“禁止已有 ENI”是另加的启动输入/管理员复核控制，不宣称仅凭 IAM 模板在所有已有 ENI 路径都能拒绝。

R3（小范围演练）：先只建 M01 和一个 T01，验证可信 SSH host key、H01 + POSIX Shell、S01/A03、报告/退出码和双路径清理。使用 AWS 认证的控制台输出/cloud-init host-key 指纹或管理员离线交付作为主机公钥可信来源；`ssh-keyscan` 只采集，不作为信任来源。确认资源登记与清理闭环后再扩展。

canary 创建后通过 DescribeInstances/DescribeNetworkInterfaces 核对实际 ENI 数量、DeviceIndex、subnet、SG、private IP 与无 public IP/association；不匹配立即停止后续创建、隔离并清理该 canary。公网 IP=true、引用已有 ENI、不同 SG 的负例必须由 IAM 拒绝或上述执行前输入核查拦截，未经负例验证不扩展。

R4（非压力矩阵）：按原计划执行18格、六平台36模板六类认证、MIN checksum 变体、双模式 noexec/ro 回退、身份/提权、安全、五个 E2E 场景及恢复。noexec/只读挂载、进程和断线等单目标功能夹具只作用于专用 FAULT VM；与 §7 的压力注入分开，既不改变宿主也不触碰生产。每次修改先留恢复点/批准的 AMI，失败时保留 RunReport 与缺陷，禁止降低断言签署。Windows 11 尚未取得时只阻塞相关 M03 条目，Linux 可继续，整体验收不能关闭。

R5（归档与清理）：最终候选绑定源码完整 SHA + 工作区补丁摘要（未提交版本不冒充 commit）、三产物及两 Goss 摘要、全部输入摘要与 UTC 时间、EnvID、RunID、命令退出码、预期/实际、执行人/复核人。使用原始归档报告，公开前去除凭据、owner token、访问令牌与私密地址。先按精确 manifest 的 instance ID、account/region 及 Project+Session 两标签核对，再终止；根卷 `DeleteOnTermination=true`，终止后轮询核对 volume/ENI。残留删除只针对本 session 的未挂载卷/未附加 ENI，保留证据所需资料后执行，不删除共享网络或基线快照。未能清理明确记 BLOCKED 并交独立清理负责人，不因测试通过忽略费用残留。

## 3. AWS CLI IAM JSON 交付与使用

这里的“CLI 权限”指 AWS 身份策略，不是凭据配置。JSON 已写入：

- [只读盘点策略模板](aws/m4-discovery-policy.template.json)：明确的 EC2 Describe、单项 Service Quotas 查询和 Access Analyzer ValidatePolicy；这些操作大多需要 `Resource: "*"`，可见指定区域内其他资产元数据，不能承诺只看带标签资源。STS caller identity 单列。
- [Linux 执行策略模板](aws/m4-execution-policy.template.json)：固定区域/账号/AMI/subnet/SG/keypair；RunInstances 分 image/基础资源、instance、volume、ENI 授权；instance 限小规格、default tenancy、on-demand、IMDSv2；volume 限加密 gp3、每卷≤40GiB；instance/volume/ENI 都须有 Project/Session，CreateTags 仅限 RunInstances 当场打标签。启停/终止、console output、残留卷/ENI删除均限定两标签和账号区域。

执行角色同时需要只读模板。所有 `REGION`、`ACCOUNT_ID`、`SESSION_ID`、`SUBNET_ID`、`SECURITY_GROUP_ID`、`KEY_PAIR_NAME` 与 `AMI_*` 都是必须替换的占位符；当前文件是有效 JSON 模板，不是已验证可直接附加的真实账户策略。AMI 清单须明确加入管理员批准的全部变体 AMI，并随版本再审；不得将 image ARN 放宽为 `image/*`。模板只支持商业 `aws` 分区，其他分区需单独修订。不得把模板附加给已有管理员用户然后声称隔离生效：应由管理员建立无其他 Allow 策略的专用短期角色，核对 SCP/permissions boundary/session policy 的交集；本模板本身不是组织级权限边界。

不授予 IAM/PassRole、SSM、S3、KMS 自定义密钥管理、Marketplace 订阅、Windows Dedicated Host/WorkSpaces、VPC/SG/路由/keypair 修改、Spot/Fleet、AMI/快照创建或共享权限。默认使用 aws/ebs；若镜像/组织要求 customer-managed KMS key，先由管理员另行审批最小 KMS 使用权限，当前执行 BLOCKED。管理员负责镜像快照准备与 Marketplace 接受条款；JSON 不覆盖这些引导职责。没有实例 profile，VM 内执行依靠 SSH，不能借本角色创建拥有 AWS 凭据的实例角色。

## 4. 验证与启用门槛

1. 离线 JSON 解析和必填占位符清单检查；逐条对照 AWS EC2 IAM RunInstances/标签创建示例及授权参考，不以 JSON 语法检查替代 IAM 行为测试。启动请求另核查新建主 ENI、无公网 IP、固定 subnet/SG、禁止已有 ENI 和额外启动覆盖参数；IAM `ec2:AssociatePublicIpAddress` 在官方 RunInstances/network-interface 条件表中有明确支持。
2. 管理员渲染模板后使用 Access Analyzer `validate-policy --policy-type IDENTITY_POLICY --policy-document file://...`，不得留下未解释 ERROR/SECURITY_WARNING；记录解析后的策略摘要。仅验证文件不是附加策略。
3. 管理员使用 IAM Policy Simulator/实际专用角色验证：允许批准 AMI/子网/SG/规格与完整标签；拒绝错误 region/account/AMI/subnet/SG、请求公网 IP、缺标签/其他 Session、超规格/超卷大小/未加密卷、修改标签/网络/IAM、操作无关实例。已有 ENI 引用必须在执行前被输入核查拦截；另核对 IAM 在具体请求上的决定，不把人工输入约束声称为 IAM 证明。`RunInstances --dry-run` 的 `DryRunOperation` 是权限预检成功；`UnauthorizedOperation` 不能算成功；dry-run 不证明配额/镜像引导/网络已可用。
4. 后续启动须单独获得用户对具体资源、许可、预算与清理安排的授权。只做一台小规格 canary 验证可启动、可连接、可清理，逐项保存真实资源 ID；再扩展到非压力矩阵。目前这些云端验证均未执行。

依据：[EC2 IAM 示例](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ExamplePolicies_EC2.html)、[创建时标签权限](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/supported-iam-actions-tagging.html)、[EC2 授权参考](https://docs.aws.amazon.com/service-authorization/latest/reference/list_ec2.html)、[AWS Windows 许可](https://docs.aws.amazon.com/prescriptive-guidance/latest/migration-microsoft-workloads-aws/licensing-microsoft-workloads.html)。模型评审通过只表示计划可进入后续执行，不代表真实 AWS 权限、Windows 许可或 M4 验收已通过。
