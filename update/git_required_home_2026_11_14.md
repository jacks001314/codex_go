# 自动 Git 必需 home

runMarketplaceGitOutputEnvWithHome 对 automatic 且无指定checkout cwd的仓库操作要求非空CodexHome，否则启动Git前返回明确错误。仅精确的单参数 --version 探测豁免，不允许额外参数借此跳过隔离。已有checkout和手动操作保持原有路径。

TestAutomaticGitRequiresHomeExceptVersionProbe验证真实版本探测通过、空/空白home均明确拒绝。修复前Git实际尝试当前目录发现仓库并报not a git repository；after plugin全包通过2.057s，vet退出0。服务的升级/刷新/curated home已在前轮传递；自定义调用者现在需明确设置home。

#61180仍待跨进程死亡回收与symlink拒绝验证；整体同步未完成。
