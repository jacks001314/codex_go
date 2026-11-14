# Trusted Git repository guard（#61180，未接入）

新增configureTrustedGitRepository(command, explicitCodexHome)：规范化home和.tmp，检查.tmp不逃逸home，拒绝.staging符号链接，创建前调用既有锁感知清理，使用MarketplaceStagingDir建立objects/refs/HEAD，为command设置GIT_DIR。初始化失败关闭guard；调用者须在Git退出后Close。

TestTrustedGitRepositoryOwnerAndReclamation通过真实磁盘与OS文件锁验证：两个活跃guard跨清理保留，释放锁但不清理的目录在下一次创建时被回收。该测试模拟进程退出释放锁，并非真正跨进程测试。

go test -count=1 ./plugin通过1.886s；go vet ./plugin退出0。代码已同步，但生产runMarketplaceGitOutputEnv尚未调用guard，因此不能声明自动远端查询/克隆已隔离。下一步需把service CodexHome传入resolver/materializer及执行器，持有guard到CombinedOutput结束，补真实Git与跨进程验证。整体未完成。
