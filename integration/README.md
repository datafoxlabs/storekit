# integration（不发布）

这是 storekit 的独立、仅含测试的消费者 module。consumer_test.go 验证三个库的组合，
drivers_test.go 以 integration build tag 验证临时 PostgreSQL/MySQL/ClickHouse；SQLite 在普通测试中覆盖。
生产库不得依赖此 module，不为 integration 打 tag。

**服务端测试选择范围：** `scripts/verify-modules.py` 的 `verify_services()` 当前通过 `-tags integration -run TestDriverIntegration` 执行服务端测试。其他使用 `integration` build tag 的 `Test*` **不会自动进入该范围**。新增服务端测试时必须检查并按需调整脚本选择器，再核对 CI 日志中实际运行的测试名称；不能仅凭 CI 绿灯声称新增测试已覆盖。

需要 Go 1.25.8、Python 3、Git 和 C 编译器；服务端验证另需 Docker。所有命令从仓库根目录执行：

| 命令 | 行为与成功条件 |
|---|---|
| make test | 只读检查元数据、格式、vet/race、消费者和驱动隔离；不连接服务端；全部 exit 0 |
| make tidy | 显式更新本地验证候选的 go.mod/go.sum，然后测试；检查 diff 后提交 |
| make test-integration | 创建三个临时 Docker 服务，运行当前选择器匹配的服务端测试，结束后清理；ClickHouse 必须为 26.2.* |
| make test-integration-ci | 仅供 GitHub Actions job 的隔离服务；不读取宿主 DSN；所有服务测试必须通过 |

三个库的公开版本与安装命令见 [README](../README.md#安装)。本 integration module 验证当前工作树，
在 `go.mod` 中依赖带 `-0.local` 后缀的本地候选；这些版本仅由根目录验证入口写入临时 Go proxy，不发布到远端。
因此开发验证应从仓库根目录执行上述命令，不能用 integration 目录内的普通 `go test` 替代。脚本在临时副本中设置 GOWORK=off，禁止 replace。
集成测试缺少任何一个服务 DSN 会失败，不会通过 skip 伪装验收成功。

本地镜像由脚本选择并记录实际 digest；CI 的 services 固定 digest，Runner 日志保存镜像身份。
ClickHouse 测试夹具关闭三项 JIT，并在运行前验证设置；该限制不改变产品驱动默认值。
临时测试会创建表、插入合成数据并验证回滚，不接受现有业务数据库。
