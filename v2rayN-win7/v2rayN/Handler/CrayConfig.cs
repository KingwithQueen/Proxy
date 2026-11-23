using System;
using System.Globalization;
using System.Linq;
using System.Net;
using Newtonsoft.Json;
using v2rayN.Mode;
using v2rayN.Resx;

namespace v2rayN.Handler
{
    internal static class CrayConfig
    {
        public static bool SupportsVmess(VmessItem node)
        {
            return node?.configType == EConfigType.VMess && GetNodeError(node) == null;
        }

        private static string GetNodeError(VmessItem node)
        {
            bool vmess = node?.configType == EConfigType.VMess;
            if ((!vmess && node?.configType != EConfigType.Trojan)
                || string.IsNullOrWhiteSpace(node.address) || string.IsNullOrEmpty(node.id)
                || node.port < 1 || node.port > 65535)
            {
                return ResUI.CheckServerSettings;
            }

            if ((!EmptyOrEquals(node.network, Global.DefaultNetwork) && !(vmess && EqualsSetting(node.network, "raw")))
                || !EmptyOrEquals(node.headerType, Global.None)
                || (!EmptyOrEquals(node.streamSecurity, Global.StreamSecurity)
                    && !(vmess && EqualsSetting(node.streamSecurity, Global.None)))
                || !string.IsNullOrWhiteSpace(node.flow)
                || !EmptyOrEquals(node.fingerprint, Global.None))
            {
                return ResUI.CrayTransport;
            }

            if (vmess && (node.alterId != 0 || !Guid.TryParseExact(node.id.Trim(), "D", out _)
                || !Global.vmessSecuritys.Contains(GetVmessSecurity(node))))
            {
                return ResUI.CrayVmessSettings;
            }

            return null;
        }

        public static string Serialize(Config config, VmessItem node, int socksPort, int httpPort)
        {
            var error = GetNodeError(node);
            if (error != null)
            {
                throw new ArgumentException(error);
            }

            if (socksPort < 0 || socksPort > 65535 || httpPort < 0 || httpPort > 65535
                || socksPort == httpPort)
            {
                throw new ArgumentException(ResUI.CrayLocalPort);
            }

            bool vmess = node.configType == EConfigType.VMess;
            bool useTls = !vmess || EqualsSetting(node.streamSecurity, Global.StreamSecurity);
            string sni = null;
            bool? verifyTls = null;
            if (useTls)
            {
                sni = node.sni?.Trim();
                if (string.IsNullOrEmpty(sni))
                {
                    sni = Utils.String2List(node.requestHost ?? "").FirstOrDefault()?.Trim();
                }
                bool allowInsecure = string.IsNullOrWhiteSpace(node.allowInsecure)
                    ? config.defAllowInsecure
                    : (bool.TryParse(node.allowInsecure, out bool flag) && flag) || node.allowInsecure.Trim() == "1";
                verifyTls = !allowInsecure;
            }

            return JsonConvert.SerializeObject(new
            {
                protocol = vmess ? "vmess" : null,
                server = NormalizeHost(node.address),
                server_port = node.port,
                password = vmess ? null : node.id,
                uuid = vmess ? Guid.ParseExact(node.id.Trim(), "D").ToString("D") : null,
                alter_id = vmess ? (int?)node.alterId : null,
                security = vmess ? GetVmessSecurity(node) : null,
                tls = vmess ? (bool?)useTls : null,
                sni = string.IsNullOrEmpty(sni) ? null : NormalizeHost(sni),
                verify_tls = verifyTls,
                socks_port = socksPort,
                http_port = httpPort,
                alpn = useTls ? node.GetAlpn() : null
            }, new JsonSerializerSettings { NullValueHandling = NullValueHandling.Ignore });
        }

        private static string GetVmessSecurity(VmessItem node)
        {
            return string.IsNullOrWhiteSpace(node.security) ? "auto" : node.security.Trim().ToLowerInvariant();
        }

        private static bool EmptyOrEquals(string value, string expected)
        {
            return string.IsNullOrWhiteSpace(value) || EqualsSetting(value, expected);
        }

        private static bool EqualsSetting(string value, string expected)
        {
            return string.Equals(value?.Trim(), expected, StringComparison.OrdinalIgnoreCase);
        }

        private static string NormalizeHost(string host)
        {
            host = host.Trim();
            if (IPAddress.TryParse(host, out var address))
            {
                return address.ToString();
            }
            return new IdnMapping().GetAscii(host);
        }
    }
}
