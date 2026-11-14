# Trusted Git 跨进程生命周期验证

新增真实子进程测试：子进程创建guard并通过stdout发布目录，持锁等待stdin关闭；父进程触发清理，确认子进程HEAD仍在；子进程os.Exit(0)不调用guard.Close；父进程等待退出后创建下一个guard，确认遗留目录被回收而父进程活跃仓库均保留。15秒context限制及失败路径kill/wait保证测试子进程生命周期。

plugin全包通过2.102s，vet退出0；详细跨进程测试通过0.04s。新增symlink staging拒绝且外部目标不被修改测试，但Windows节点创建symlink报缺少权限，因此该项SKIP，不能声称已验证。记录见trusted_git_process_detail_2026_11_14.log。无需改变系统权限，后续在具备symlink能力的节点执行。

#61180核心生产创建/执行/回收和本机跨进程测试已落地；symlink实际测试仍缺，Linux原生及整体最终回归待完成。整体同步仍未完成、未commit/push。
