# M4 AWS 接入预检（2026-11-08）

范围：恢复执行除 Windows 11 与压力测试外的 M4 Goal。当前完成只读API、Access Analyzer与无副作用dry-run；尚未创建资源或产生测试实例费用。

## 权限与容量实测

- STS返回账号`891376991548`，区域`us-east-1`返回`opt-in-not-required`。
- 两份已交付客户托管策略分别调用`accessanalyzer validate-policy`，findings均为空数组；没有ERROR/SECURITY_WARNING。
- 带Project/Session标签、CIDR `10.77.0.0/16`、default tenancy的`ec2 create-vpc --dry-run`返回`DryRunOperation`，没有创建VPC。这只证明本次VPC请求的权限预检，不证明所有实际创建/清理API均已通过。
- 标准按需实例配额（`L-1216C47A`）为16 vCPU；区域内pending/running/stopping/stopped实例列表为空。
- t3.small/t4g.small/t3.medium/t4g.medium均2 vCPU；t3.xlarge为4 vCPU。五种规格均在us-east-1a有offering，实际实时容量仍以启动返回为准。
- 已有VPC CIDR为`192.168.0.0/16`和`172.31.0.0/16`，不修改或接管；候选测试CIDR `10.77.0.0/16`不重叠。本会话没有已导入测试keypair。

## 已核验候选镜像

所有下列AMI均available/public、发布账号正确、无ProductCodes，无需启用Marketplace回退。

| EnvID | OS / 架构 | AMI | root device |
|---|---|---|---|
| T01 | Ubuntu 24.04 x86_64 | ami-0d6dd9e8265d48906 | /dev/sda1 |
| T02 | Ubuntu 24.04 arm64 | ami-09a93d801db0bafae | /dev/sda1 |
| T03 | Debian 13 x86_64 | ami-0412e376e50785f2a | /dev/xvda |
| T04 | Debian 13 arm64 | ami-0d312ec202547c16c | /dev/xvda |
| T05 | Rocky 9.8 x86_64 | ami-07f1ef003bc5de2b1 | /dev/sda1 |
| T06 | Rocky 9.8 arm64 | ami-07e6c22e0dbbc9683 | /dev/sda1 |

Ubuntu/Debian原始root为8GiB，Rocky为10GiB。Rocky原始gp2，所有原始root未加密；启动请求必须显式覆盖为加密gp3、aws/ebs、3000 IOPS、125MiB/s、DeleteOnTermination=true，不能沿用镜像默认。Ubuntu额外列出ephemeral0/1映射，候选t3/t4g无instance store，构造输入时明确核对并禁用无用映射。

## 配额内编排和费用（用户已批准）

保持最多9台在册且最多16 vCPU运行：

1. canary/构建：一台t3.xlarge（B01）加一台t4g.small（ARM目标），共6 vCPU；先验证建立、SSH、H01与清理。
2. 浏览器驱动运行时：B01 + 一个管理端 + S01 + 四个运行目标，共16 vCPU；其余目标可停机，仍计入9台在册。
3. 六目标混合Run：停止B01，运行一个管理端 + S01 + 六目标，共16 vCPU；归档后缩减目标、恢复驱动检查同源报告。M01/M02顺序替换，保留基线、输入摘要与证据，不同时跑齐超配额组合。

Price List API现场返回（effective 2026-11-01，USD）：t3.small 0.0208/h；t4g.small 0.0168/h；t3.medium 0.0416/h；t4g.medium 0.0336/h；t3.xlarge 0.1664/h；gp3 0.08/GB-month；标准snapshot 0.05/GB-month；使用中的公网IPv4 0.005/IP-hour。

以730小时/月折算EBS的近似基础费用：canary两台、120GiB root、两个公网IP约0.2064/h；六目标阶段含400GiB在册root与一个公网IP约0.2240/h；浏览器四目标阶段含400GiB root与两个公网IP约0.3578/h。以上不是最终账单，尚需计入快照实际存储、传输、T系列Unlimited可能发生的CPU credit费用和税费，不假设免费额度。

用户已在2026-11-08明确批准本轮预算上限提高至30美元并恢复Goal。按阶段建立、验收和及时回收，接近预算时停止新建；30美元是执行成本上限，不由IAM硬性保证，也不是自动断电限制。此前8小时是建议的最长保留上限，不是运行要求，不为凑满时限空跑。按清单/时间/费用估算跟踪并预留清理余量。未完成用例如实保留待测，不冒充Goal完成。实际执行及权限阻塞见[canary记录](m4-aws-canary-20261108.md)。
