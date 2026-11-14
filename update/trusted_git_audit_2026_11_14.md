# #61180 trusted Git 回收审计与临时配置泄漏修复

上游 c37f817 将 configure_trusted_git_repository 的 TempDir 改为 MarketplaceStagingDir：canonical Codex home/.tmp/.staging、拒绝staging symlink、创建前清理弃置目录、owner锁持续到Git退出。

Go已有MarketplaceStagingDir及锁清理，但runMarketplaceGitOutputEnv未建立临时trusted repository，只提供isolatedPluginGitEnv。因此不能仅把某个MkdirTemp替换为staging就宣称#61180完成。缺口包括：automatic clone/remote lookup的trusted GIT_DIR建立与生命周期、Codex home传递、repo环境变量清理完整性、跨进程活跃/退出回收测试。

本轮消除相关的既有泄漏：isolatedPluginGitEnv通过sync.Once创建系统临时目录和empty-gitconfig，但从不回收。改用os.DevNull作为Git空全局配置源，删除该目录创建和全局缓存变量。保留禁用system config和trusted PATH行为。新增真实Git config --file os.DevNull --list验证，输出为空并退出0。

go test -count=1 ./plugin通过1.631s，go vet ./plugin退出0。#61180仍待接入trusted repository，整体同步未完成，未提交/推送。
