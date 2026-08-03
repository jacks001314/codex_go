---
name: vscode
description: 需要测试go版本vscode codex插件的支持情况。
---


## 工作流

1. 使用scripts/build.ps1构建go版本的codex.exe,codex-code-mode-host.exe 二进制文件。

2. 备份C:\Users\huoga\.vscode\extensions\openai.chatgpt-26.727.40816-win32-x64\bin\windows-x86_64目录下的codex.exe和codex-code-mode-host.exe文件。

3. 使用新的go版本二进制文件替换备份的文件。

4. 打开vscode 打开codex插件，然后进行各种测试场景测试，找出各种问题，并修复，验证。
