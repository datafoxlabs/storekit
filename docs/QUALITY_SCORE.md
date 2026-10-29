# 质量检查与剩余验收

本页列出可重复的检查入口，不把尚未发布或尚未接入的工作标成完成。实现证据以对应提交的测试输出和 GitHub Actions 结果为准。

| 范围 | 验证入口 | 验收边界 |
|---|---|---|
| 四 module 可独立消费 | make test | 临时候选 proxy；tidy/no-diff；无 workspace/replace |
| Go 格式、静态检查、数据竞争 | make check-fmt / make test | gofmt、vet、race |
| Python 验证边界 | make check-tools | CI 目标门禁、元数据漂移、夹具失败清理、依赖方向 |
| 服务端行为 | make test-integration | 临时 PostgreSQL/MySQL/ClickHouse；SQLite 在普通测试中 |
| 驱动隔离 | make test | 核心及各单驱动的包依赖和二进制依赖 |
| Harness 与 Spec 完整性 | make check-harness | 官方固定 revision 校验器；不代表运行时 Hook 信任 |
| 远端持续检查 | .github/workflows/verify.yml | 以具体 workflow/job 结果为准，配置存在不等于 CI 通过 |
| 实际使用方与发布 | 使用方验收记录与 GitHub tags | 实际使用方接入尚未验收；无正式 tag |

修改相应 owning source 后重新执行相关入口；禁止用历史成功覆盖当前失败。默认 JIT 的 ClickHouse ARM 稳定性
不在当前夹具通过声明内，具体限制见 RELIABILITY.md。
