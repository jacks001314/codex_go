# 自动插件 Git 仓库环境隔离

按 Rust core-plugins/git_policy.rs 的 REPOSITORY_LOCAL_GIT_ENVIRONMENT_VARIABLES 补齐自动Git环境清理：GIT_DIR/WORK_TREE/COMMON_DIR/INDEX_FILE、对象目录、alternates、namespace、prefix、graft/shallow、仓库发现设置及GIT_CONFIG均移除。已有scoped config清理保留。Windows环境键先大写以匹配系统不区分大小写语义；自动操作固定GIT_OPTIONAL_LOCKS=0。手动操作分支未改。

TestIsolatedPluginGitEnvDropsRepositoryRedirects 注入15个虚构重定向变量，修改前15项均残留，见before日志。修改后go test -count=1 ./plugin通过1.676s，go vet ./plugin退出0；after/vet日志已同步。

这是trusted repository接入的前置隔离修复，不等于#61180回收完成。仍需传递Codex home、建立受信任GIT_DIR、持有staging owner锁直到Git退出，以及跨进程回收验证。整体同步未完成。
