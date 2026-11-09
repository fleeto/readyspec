# M4 AWS 权限文件

权限范围摘要与后续配置流程见 [M4 AWS 账号权限备用说明](m4-permissions-summary.md)。

用户最新要求是由代理准备、验证和回收除 Windows 11 与压力测试外的全部 Linux 资源。

## 当前推荐：两份客户托管策略

用于本次全量覆盖的独立文件：[m4-linux-compute-policy-v2-exact-amis.json](m4-linux-compute-policy-v2-exact-amis.json)。这是完整Compute策略，与当前`m4-linux-compute-policy.json`内容一致，包含六个精确官方AMI ARN。将其全部内容覆盖到`ReadySpecM4Compute`的JSON编辑框，保存并设为默认版本；NetworkRead策略继续保留。

IAM用户的所有内联策略合计只有2,048字符上限，拆成多个用户内联策略不能解决。请在 **IAM → 策略 → 创建策略 → JSON** 分别创建下面两份客户托管策略，再把两份同时附加到AWS CLI所用身份：

| 建议策略名 | 文件 | 去空白字符数 / 单份上限 |
|---|---|---|
| ReadySpecM4NetworkRead | [网络与盘点](m4-linux-network-read-policy.json) | 3,169 / 6,144 |
| ReadySpecM4Compute | [实例、镜像及创建标签](m4-linux-compute-policy.json) | 5,524 / 6,144 |

两份合起来与下面已填值完整文件的18个Statement逐项完全相同。必须同时附加：网络创建也依赖Compute文件内的CreateTags授权。单份完整文件保留用于专用角色inline policy或核对，不能直接保存成用户inline policy，也超过单份客户托管策略大小。

2026-11-08 实际创建网络成功，但首次`RunInstances`在官方Ubuntu AMI资源处返回无identity-based allow，尚未启动实例。当前Compute文件是修复候选：将`UsePublicPublisherImages`收窄为已核验的六个AMI精确ARN，保留public/ebs/machine条件，去掉该语句的`ec2:Owner`条件；其他17条语句不变。IAM的Owner条件可使用`amazon`等别名，不能直接假定它与DescribeImages返回的数值OwnerId一致；当前没有解码权限，具体不匹配条件尚未证实。修复需在 **IAM → 策略 → ReadySpecM4Compute → 编辑 → JSON** 更新并设为默认版本；无需重建或更新NetworkRead策略。Access Analyzer findings为空，真正启动能力仍待更新后的dry-run与canary验证。精确AMI更换时需重新核验来源并更新清单。真实创建/回收记录见[执行记录](../m4-aws-canary-20261108.md)。

已填值文件：[m4-full-linux-policy.json](m4-full-linux-policy.json)。账号由AWS CLI STS读取为`891376991548`；CLI未设置默认区域，本轮明确选`us-east-1`而非声称读取到该默认值；会话`d6b321143e61d7bf2a6c6b5f`。该文件无占位符，关闭可选Marketplace路径，并用`Null ec2:ProductCode=true`拒绝带产品代码的镜像；仅使用官方公开无产品代码AMI和本会话派生镜像。如必须使用Marketplace，须另行填入经核验的精确镜像/产品数据。该收紧不扩大原评审权限。

当前CLI已附加两份客户托管策略，STS、区域、配额、镜像查询、Access Analyzer、专用网络/公钥的真实创建和删除均已验证。实际实例启动存在上述权限阻塞，不能把VPC dry-run通过视为计算权限已全部通过。本轮未修改IAM或AWS全局配置。

- 当前完整范围：[m4-full-linux-policy.template.json](m4-full-linux-policy.template.json)，对应 [完整 Linux 资源计划](../m4-aws-full-linux-plan.md)；Pi + Kimi 3.1 Pro 评审记录见 [评审记录](../m4-aws-full-linux-review-k3.1-pro.md)。须渲染后作为专用角色 inline policy 使用。
- 旧版 [m4-aws-cli-policy.template.json](m4-aws-cli-policy.template.json) 只是已评审盘点/执行策略的合并，依赖预置网络和镜像，**不覆盖当前完整资源准备要求**。
- `m4-discovery-policy.template.json`、`m4-execution-policy.template.json`、`m4-launch-request.template.json` 保留为旧版拆分策略与私网启动输入参考，不应据此声称获得完整资源控制。

这些是 IAM 身份策略和请求模板，不是 AWS 凭据。文件写入和模型评审不等于已经附加策略或完成真实 AWS 验证。
