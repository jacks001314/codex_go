# Linux原生验证准备

Master节点目录确认Linux x86_64节点jacks在线；另一Linux节点192.168.191.15离线。当前工具没有跨节点执行入口，已向用户请求在jacks创建专用验证子Agent的明确授权；尚未创建、未发送任务、未修改任何其他Agent生命周期。

新增seccomp_native_linux_amd64_test.go：每个网络模式在独立子进程内加载真实TSYNC seccomp，避免污染父测试进程。覆盖restricted/proxy/proxy_unix/vm模式；检查普通getpid存活；检查native及x32标志syscall的io_uring/request_key/网络拒绝，以及受禁socket地址族返回EPERM。此测试用于验证真实内核路径，不以交叉编译替代原生结果。

Windows本地已执行GOOS=linux GOARCH=amd64 go test -c -o update/linuxsandbox_native.test ./sandbox/linuxsandbox，退出0；随后恢复GOOS/GOARCH环境。二进制仅本地准备，不同步或提交。源码已同步Master。

原生执行命令：go test -count=1 -v ./sandbox/linuxsandbox -run '^TestNativeNetworkSeccomp$'。需要在Linux节点实际运行；当前没有原生通过结果。上游Linux仓库pull同样待执行。

全模块测试29603仍在运行；本轮新增文件只针对Linux，不进入Windows测试编译。整体同步、Linux认证、commit/push尚未完成。
