# Windows 7 SP1 使用说明

## v2rayN 客户端

### 1. 环境准备与启动
1. 确保系统已安装 **.NET Framework 4.8**（可下载[微软官方离线安装包](https://support.microsoft.com/zh-cn/servicing/dotnetframework/2019/10/microsoft-net-framework-4-8-offline-installer-for-windows)）。
2. 根据系统位数选择 `x64` 或 `x86` 发布包，将压缩包内所有文件完整解压至同一可写目录（切勿直接在压缩包内运行，确保 `Cray.exe` 与 `v2rayN.exe` 位于同一目录下）。
3. 运行 `v2rayN.exe`，双击系统托盘图标打开主窗口。

### 2. 添加与启用节点
- **导入节点**：复制 `trojan://` 节点链接后在主界面按 `Ctrl+V` 即可导入；也可通过顶部「服务器」菜单手动添加 Trojan 节点。
- **启动代理**：在列表中选中节点并设为活动节点（或按 `Enter`）。内核启动后，可在主窗口底部或托盘菜单中开启「系统代理」。
- **本地监听端口**：默认在 `127.0.0.1` 提供 SOCKS5（端口 `10808`）与 HTTP（端口 `10809`）代理。若修改过端口设置，请以主窗口底部状态栏显示的实际端口为准。

### 3. 节点检索与延迟测试
- **搜索过滤**：按 `Ctrl+F` 可按节点别名或服务器地址快速过滤列表。
- **批量真连接测速**：点击主界面「一键测试真连接延迟」，客户端会并发通过列表中的节点请求测试 URL，并将实测延迟更新至节点列表；测速过程中再次点击该按钮可随时中止。
- **单选/多选测速**：选中一个或多个节点后按 `Ctrl+R`（或通过右键菜单），可仅对所选节点测试真连接延迟。

### 4. Cray 内核特性与证书校验
- **协议范围**：Cray 专用于标准 TCP + TLS 传输的 Trojan 节点，本地入站支持 SOCKS5 `CONNECT`、HTTP `CONNECT` 及普通 HTTP 代理转发。
- **TLS 证书校验**：默认开启服务端证书链与主机名（SAN）校验。
  - 若节点地址填写的是 IP，而服务端证书签发给域名，请在节点配置中填写对应的 **SNI**。
  - 若服务端使用自签名或私有 CA 签发的证书，可将根证书导入 Windows「受信任的根证书颁发机构」存储，或在独立运行时通过 `ca_file` 指定。

---

## 独立运行 Cray 内核

通过 v2rayN 启动时，客户端会自动运行 `Cray.exe -config -` 并通过标准输入（stdin）传入配置。若无需 GUI，也可在命令行独立运行 Cray：

```powershell
Cray.exe -config config.json
```

配置文件需采用 **UTF-8** 编码，示例格式如下：

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

### 配置项说明

| 字段 | 类型 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `server` | `string` | 是 | - | Trojan 服务端域名或 IPv4/IPv6 地址 |
| `server_port` | `uint16` | 否 | `443` | 服务端端口（`1`–`65535`） |
| `password` | `string` | 二选一 | - | 节点明文密码（与 `password_sha224` 二选一） |
| `password_sha224` | `string` | 二选一 | - | 预先计算的 56 位十六进制 SHA-224 密码哈希 |
| `sni` | `string` | 否 | 同 `server` | TLS 握手与证书校验使用的主机名 |
| `verify_tls` | `bool` | 否 | `true` | 是否校验服务端 TLS 证书（建议保持开启） |
| `socks_port` | `uint16` | 否 | `10808` | 本地 `127.0.0.1` SOCKS5 监听端口，设为 `0` 则禁用 |
| `http_port` | `uint16` | 否 | `10809` | 本地 `127.0.0.1` HTTP 监听端口，设为 `0` 则禁用 |
| `alpn` | `string[]` | 否 | 不发送 | TLS ALPN 协议列表（最多 16 项，如 `["h2", "http/1.1"]`） |
| `ca_file` | `string` | 否 | 系统根证书 | 自定义 CA 证书路径（支持 PEM 或 DER 格式）；未指定时使用 Windows 系统证书存储校验 |

> **注意**：`socks_port` 与 `http_port` 不能相同，且不能同时设为 `0`（至少需保留一个本地监听端口）。
