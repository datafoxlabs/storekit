# integration（不发布）

这是 storekit 的独立、仅含测试的消费者 module。consumer_test.go 验证三个库的组合，
drivers_test.go 以 integration build tag 验证临时 PostgreSQL/MySQL/ClickHouse；SQLite 在普通测试中覆盖。
生产库不得依赖此 module，不为 integration 打 tag。

需要 Go 1.25.8、Python 3、Git 和 C 编译器；服务端验证另需 Docker。所有命令从仓库根目录执行：

| 命令 | 行为与成功条件 |
|---|---|
| make test | 只读检查元数据、格式、vet/race、消费者和驱动隔离；不连接服务端；全部 exit 0 |
| make tidy | 显式更新未发布候选的 go.mod/go.sum，然后测试；检查 diff 后提交 |
| make test-integration | 创建三个临时 Docker 服务，验证后清理；ClickHouse 必须为 26.2.* |
| make test-integration-ci | 仅供 GitHub Actions job 的隔离服务；不读取宿主 DSN；所有服务测试必须通过 |

脚本在临时副本中使用 GOWORK=off 和本地 Go proxy，禁止 replace。候选版本尚未发布，因此此阶段不承诺
在源码目录直接执行 GOWORK=off go test 能从远端下载这些版本；根目录入口负责提供候选包。
集成测试缺少任何一个服务 DSN 会失败，不会通过 skip 伪装验收成功。

本地镜像由脚本选择并记录实际 digest；CI 的 services 固定 digest，Runner 日志保存镜像身份。
ClickHouse 测试夹具关闭三项 JIT，并在运行前验证设置；该限制不改变产品驱动默认值。
临时测试会创建表、插入合成数据并验证回滚，不接受现有业务数据库。
