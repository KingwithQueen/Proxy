# Jray (Xray-core + Native AnyTLS)

Jray 基于官方 `XTLS/Xray-core`，仅新增原生实现的 **AnyTLS**（v1/v2）入站与出站协议适配，无任何第三方外部依赖或 GPL 代码引入，完整兼容 **Linux** 与 **Windows**。

## 特性摘要

- **协议支持**：AnyTLS v2（含 `cmdSYNACK` 建连状态回传、`cmdServerSettings` 版本协商、服务端 `cmdUpdatePaddingScheme` 动态下发、`cmdHeartRequest/Response` 心跳保活、v1 客户端向后兼容）
- **零额外依赖与低开销**：直接复用 Xray-core `common/buf`、`common/bytespool`、`transport/pipe` 与 `DispatchLink`，合并握手与填充帧至单次 TLS Write，避免额外分片指纹
- **传输层与安全性**：
  - 原生兼容 TLS 与 REALITY
  - 入站支持多用户 SHA-256 认证、`UserManager`（支持 gRPC API 动态增删用户）及 Trojan 风格 `fallbacks`
  - 集成 `freedom` 默认私网拦截规则及出站传输层安全校验（公开地址需启用 TLS / REALITY）
- **UDP 转发**：完整支持 UoT v2（`sp.v2.udp-over-tcp.arpa`，Full-Cone NAT 与 `isConnect=false/true`）与 UoT v1

---

## 编译说明

- `build_windows.bat`：Windows 一键编译脚本（输出 `jray.exe`）

### Windows 编译步骤

需安装 **Go 1.27+**（或保留联网以由 `GOTOOLCHAIN=auto` 自动获取匹配工具链），在 `Jray/` 目录执行：

1. 直接双击或命令行运行：
   ```bat
   build_windows.bat
   ```
2. 或手动执行：
   ```powershell
   $env:CGO_ENABLED="0"
   $env:GOOS="windows"
   $env:GOARCH="amd64"
   go build -o jray.exe -trimpath -ldflags "-s -w -buildid=" ./main
   ```

### Linux 编译命令

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o jray -trimpath -ldflags "-s -w -buildid=" ./main
```

---

## AnyTLS JSON 配置示例

### 服务端（AnyTLS Inbound + TLS + Fallback）

```json
{
  "log": {
    "loglevel": "warning"
  },
  "inbounds": [
    {
      "listen": "0.0.0.0",
      "port": 443,
      "protocol": "anytls",
      "settings": {
        "users": [
          {
            "password": "your-strong-password",
            "email": "alice@example.com",
            "level": 0
          }
        ],
        "paddingScheme": [
          "stop=8",
          "0=30-30",
          "1=100-400",
          "2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000",
          "3=9-9,500-1000",
          "4=500-1000",
          "5=500-1000",
          "6=500-1000",
          "7=500-1000"
        ],
        "fallbacks": [
          {
            "dest": "127.0.0.1:8080",
            "xver": 1
          }
        ]
      },
      "streamSettings": {
        "network": "tcp",
        "security": "tls",
        "tlsSettings": {
          "certificates": [
            {
              "certificateFile": "/path/to/fullchain.pem",
              "keyFile": "/path/to/privkey.pem"
            }
          ]
        }
      }
    }
  ],
  "outbounds": [
    {
      "protocol": "freedom"
    }
  ]
}
```

### 客户端（SOCKS/HTTP -> AnyTLS Outbound + TLS）

> 注：与上游 Xray-core 一致，`allowInsecure` 已被移除；使用正规 CA 证书时配置 `serverName` 即可，若测试自签证书请使用 `pinnedPeerCertSha256`（证书 DER 的 SHA-256 十六进制值）。

```json
{
  "log": {
    "loglevel": "warning"
  },
  "inbounds": [
    {
      "listen": "127.0.0.1",
      "port": 10808,
      "protocol": "socks",
      "settings": {
        "udp": true
      }
    }
  ],
  "outbounds": [
    {
      "protocol": "anytls",
      "settings": {
        "address": "your.server.domain",
        "port": 443,
        "password": "your-strong-password",
        "idleSessionCheckInterval": 30,
        "idleSessionTimeout": 30,
        "minIdleSession": 1
      },
      "streamSettings": {
        "network": "tcp",
        "security": "tls",
        "tlsSettings": {
          "serverName": "your.server.domain",
          "fingerprint": "chrome"
        }
      }
    }
  ]
}
```
