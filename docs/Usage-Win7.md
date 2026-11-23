# 在 Windows 7 SP1 上使用 Proxy

## v2rayN 客户端

1. 按系统类型选择 x64 或 x86 构建包，将所有文件解压到同一个可写目录，并安装 .NET Framework 4.8。 [微软离线安装程序](https://support.microsoft.com/zh-cn/servicing/dotnetframework/2019/10/microsoft-net-framework-4-8-offline-installer-for-windows)
2. 运行 `v2rayN.exe`，从托盘打开主窗口。复制已有的 `trojan://` 链接后按 `Ctrl+V` 导入，或者通过服务器菜单手动添加 Trojan 节点。
3. 选中节点并点击「设为活动」。内核启动后，可从主窗口或托盘启用系统代理。应用也可以手动连接本机 HTTP 或 SOCKS5 端口。默认地址为 `127.0.0.1`，端口分别为 `10809` 和 `10808`，自定义值以窗口显示为准。

按 `Ctrl+F` 搜索节点名或地址。点击「一键测试真连接延迟」会通过当前列表中的各个可测速节点请求测试 URL，结果显示在节点列表中。再次点击可取消。在列表中按 `Ctrl+R`，或使用右键菜单，可只测试选中的节点。

Cray 支持 Trojan TCP/TLS 和回环地址上的 HTTP CONNECT、普通 HTTP、SOCKS5 CONNECT。默认验证服务端证书和主机名。若用 IP 地址连接持有域名证书的服务器，请填入对应的 SNI。私有 CA 可导入 Windows 受信任的根证书存储。

## 独立运行 Cray

v2rayN 会启动 Cray 并通过标准输入传入配置。独立运行时可执行 `Cray.exe -config config.json`，配置文件采用 UTF-8。

```json
{
  "server": "trojan.example.com",
  "server_port": 443,
  "password": "替换为节点密码",
  "sni": "trojan.example.com",
  "verify_tls": true,
  "socks_port": 10808,
  "http_port": 10809
}
```

`alpn` 可选，值为字符串数组。`ca_file` 可选，指定 PEM 或 DER 格式的 CA 文件。监听端口设为 `0` 即禁用，至少保留一个有效监听。
