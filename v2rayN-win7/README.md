# v2rayN Win7

Windows 7 SP1 桌面客户端。基于 [v2rayN 5.39](https://github.com/2dust/v2rayN/releases/tag/5.39) 的源码（提交 529b661），包含 Cray 内核集成、主界面更新和一键真实连接延迟测试。客户端使用 Windows Forms 和 .NET Framework 4.8。许可证见 [LICENSE](LICENSE)。

在仓库根目录运行 `.\build-win7.ps1` 可构建 Cray 和客户端发布包。Visual Studio 解决方案为 `v2rayN.sln`，客户端项目为 `v2rayN/v2rayN.csproj`。解决方案中还保留了上游的 `v2rayUpgrade` 项目。

`v2rayN/Resources/sysproxy.exe.gz` 和 `v2rayN/Resources/sysproxy64.exe.gz` 是上游 5.39 的原始二进制资源。编译时会将它们嵌入客户端，用于在 32 位和 64 位 Windows 上设置系统代理。
