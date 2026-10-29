# 安全边界

宿主拥有数据库身份、网络访问和授权。storekit 不为业务数据库创建账号，不替使用方选择生产目标。
连接信息可能包含凭据；不要提交真实 DSN、密码、token 或访问授权链接。

- 本地 integration 只创建随机命名的临时 Docker 容器，端口绑定 loopback，使用合成凭据，结束清理容器及匿名卷。
- CI integration 只连接 .github/workflows/verify.yml 中的 postgres/mysql/clickhouse 服务别名；脚本覆盖继承的测试 DSN。
  `--ci-services` 在 GITHUB_ACTIONS 非 true 时在任何子进程前失败。不接受业务环境 DSN 作为便捷覆盖。
- CI 没有发布、部署或发 tag 作业；服务镜像固定 digest。维护者变更镜像时重新验证最低版本。
- 候选模块仅写入临时 Go proxy，不向 Go registry 或 GitHub 发布。GOWORK=off、禁止 replace 和依赖边界检查是独立消费验证条件。
- Harness 验证工具固定 RepoFoundry 0.15.0 的 commit；规范由本地 lock/digest 验证，不在日常 CI 中更新 Catalog。
- CODEOWNERS 指向已核实的项目 Maintainer @XiaoWeiKIN；审批是否强制仍取决于 GitHub 分支保护设置。
- Codex Hook 配置已生成；宿主是否信任并加载不由仓库文件证明。无 Hooks 时使用显式 Router 流程。

本仓库未完成独立渗透测试或生产安全评估。依赖漏洞治理与生产授权由维护团队和宿主项目负责。
