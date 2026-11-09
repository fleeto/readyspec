# M4 AWS 执行计划与 IAM 模板评审

主设计者：Codex；独立评审者：通过 Pi 1.5.0 调用的 `magpie / kimi-code-cn/k3.1-pro`。本轮按用户要求评审非压力 AWS 执行计划与实际写入的 IAM JSON；不部署 IAM，不创建收费资源。

状态：**两轮评审通过，双方达成一致**。首轮 P1-01 修订后，第二轮确认关闭且无新增阻断；Codex 核对官方依据与文件差异后接受。通过仅涉及计划和模板，不代表实际 AWS 权限或 M4 实机验收已通过。

评审对象：

- [AWS 非压力执行计划](m4-aws-execution-plan.md)
- [只读盘点 IAM JSON](aws/m4-discovery-policy.template.json)
- [Linux 执行 IAM JSON](aws/m4-execution-policy.template.json)
- [M4 总验收矩阵](m4-acceptance-plan.md)

## 调用与初稿

模型目录实际列出 `magpie kimi-code-cn/k3.1-pro`，未使用 highspeed 或其他模型。禁用 Pi 工具、MCP、扩展、技能和自动上下文发现，仅显式发送评审文件。原始输出与提示位于 `/tmp/opencode/readyspec-m4-aws-review-pACNSu/`。

```sh
pi --provider magpie --model kimi-code-cn/k3.1-pro --thinking medium \
  --no-extensions --no-mcp --no-skills --no-prompt-templates \
  --no-themes --no-context-files --no-tools --no-session --print \
  @round1-prompt.md @docs/m4-aws-execution-plan.md \
  @docs/aws/m4-discovery-policy.template.json \
  @docs/aws/m4-execution-policy.template.json @docs/m4-acceptance-plan.md
```

首轮输入摘要：

| 文件 | SHA-256 |
|---|---|
| 执行计划 | `981482aa3f6fae865440ee8ced0aba54f797328af30cd97605c2cb5ff65edc40` |
| 盘点策略 | `24e1fb342a2f64b5042b4ac49e3680a15d3f53c944f21332b4d8dba971ea93fa` |
| 执行策略 | `ff881ba95531663567ebdf0491daebe4c6f48aaaf3d6b439f83ce8cea5d7e3bd` |

## 本地验证

两份模板均通过 Python JSON 解析、Version/Statement/Sid 唯一性检查。执行策略仅含明确 EC2 动作，所有 Allow 资源均限 ARN，未使用 `Action: "ec2:*"` 或 `Resource: "*"`。盘点策略的全资源读取是明确披露的必要能力，不承诺标签级隐藏其他区域内资产元数据。

对照 AWS [EC2 授权参考](https://docs.aws.amazon.com/service-authorization/latest/reference/list_ec2.html) 核查 RunInstances 的 instance/volume/ENI 条件，以及 GetConsoleOutput/DeleteVolume/DeleteNetworkInterface 的标签条件；另核对 [RunInstances 示例](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ExamplePolicies_EC2.html) 和 [aws/ebs 默认密钥权限](https://docs.aws.amazon.com/ebs/latest/userguide/ebsapi-permissions.html)。这些是离线文档核查，不代表 Access Analyzer、Policy Simulator 或真实 IAM 角色调用已通过。

官方 EC2 授权表中找到策略包含的全部24个 EC2 动作。新增启动 JSON 通过 AWS CLI `--generate-cli-skeleton output` 离线输入形状校验，命令设置 `AWS_EC2_METADATA_DISABLED=true`、`--no-sign-request`，未发送 AWS API 请求；这不是 dry-run，也不是实际启动/权限证明。

## 评审结果

首轮结论：**暂不通过**，P0 无、P1 一项、P2 无新增必须修订。未关闭项：P1-01（RunInstances 尚未完整约束私网网卡创建边界）。

| 编号 | Kimi 意见 | 主设计者回应与修订 |
|---|---|---|
| P1-01 | ENI 授权缺少 AssociatePublicIpAddress=false；允许指定 SG 不等于封住已有 ENI 引用路径；须增加启动前负例与创建后实际网卡核对 | 接受。执行策略新增公网 IP Bool；新增完整单实例启动请求 JSON，只创建主 ENI、固定 subnet/SG、不分配公网 IP。R2 明确禁止已有 ENI/额外 ENI/launch template/额外 CLI 覆盖，人工输入复核控制和 IAM 强制约束分开说明；R3/§4 补公网 IP、已有 ENI、错误 SG 负例与实际 ENI/SG/public IP 核对 |

其余范围首轮认为已形成闭环：非压力范围、Windows11/Server CI 区别、Playwright 历史/当前证据、RunInstances 分资源授权、专用角色及预算/清理、模板占位符与后续真实账户验证门槛。修订后已由同一 provider/model 完成复审。

第二轮重新发送完整计划、两策略 JSON、启动请求 JSON、首轮原文、逐项回应、AWS 授权表核对结果和总验收矩阵。第二轮输入摘要：

| 文件 | SHA-256 |
|---|---|
| 执行计划 | `ec1cea3704f2475d19b2d2e8c03b74d3c421f956aea2e5b7f8dda113add222f6` |
| 盘点策略 | `24e1fb342a2f64b5042b4ac49e3680a15d3f53c944f21332b4d8dba971ea93fa` |
| 执行策略 | `30a765e8257ee9a7944bf138eaac71a0276b51d52f0fe25456336330f0ff4ac9` |
| 启动请求 | `d18b4e481246f93ae7d55bf73cb4a303f0ae87a44c8491a98d521490946716fd` |

## 第二轮原始结论与采纳

> **评审通过，无未关闭设计阻断；仅计划与策略模板通过，不代表 AWS 实际权限或 M4 实机验收完成**

模型逐项确认 P1-01 关闭：

1. `LaunchTaggedInterfacesInApprovedSubnet` 已补 `ec2:AssociatePublicIpAddress=false`，并核查官方 action/resource 支持。
2. 启动 JSON 只新建受批私网主 ENI，固定 subnet/SG；既有 ENI 禁令是明确的输入与管理员复核控制，没有伪称 IAM 自动证明。
3. 公网 IP、已有 ENI 和错误 SG 的负例，以及 canary 后实际 ENI/SG/地址核对均成为扩展门槛。
4. 单 root EBS、40GiB、ClientToken 及标签修订未削弱原边界；无新增 P0/P1/P2 设计阻断。

第二轮同时保留真实云端验证门槛：渲染后 Access Analyzer、Policy Simulator/专用角色验证、dry-run、负例、canary 属性与清理闭环尚未执行。主设计者接受评审结论；复审后只更新执行计划状态与评审记录，不改策略内容。表中计划摘要是更新通过状态前实际提交评审的版本，三个 JSON 的摘要与交付文件相同。
