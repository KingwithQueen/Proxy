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
        public static string Serialize(Config config, VmessItem node, int socksPort, int httpPort)
        {
            if (node?.configType != EConfigType.Trojan
                || string.IsNullOrWhiteSpace(node.address) || string.IsNullOrEmpty(node.id)
                || node.port < 1 || node.port > 65535)
            {
                throw new ArgumentException(ResUI.CheckServerSettings);
            }

            if (!EmptyOrEquals(node.network, "tcp")
                || !EmptyOrEquals(node.headerType, "none")
                || !EmptyOrEquals(node.streamSecurity, Global.StreamSecurity)
                || !string.IsNullOrWhiteSpace(node.flow)
                || !EmptyOrEquals(node.fingerprint, "none"))
            {
                throw new ArgumentException(ResUI.CrayTransport);
            }

            if (socksPort < 0 || socksPort > 65535 || httpPort < 0 || httpPort > 65535
                || socksPort == httpPort)
            {
                throw new ArgumentException(ResUI.CrayLocalPort);
            }

            string sni = node.sni?.Trim();
            if (string.IsNullOrEmpty(sni))
            {
                sni = Utils.String2List(node.requestHost ?? "").FirstOrDefault()?.Trim();
            }

            bool allowInsecure = string.IsNullOrWhiteSpace(node.allowInsecure)
                ? config.defAllowInsecure
                : (bool.TryParse(node.allowInsecure, out bool flag) && flag) || node.allowInsecure.Trim() == "1";

            return JsonConvert.SerializeObject(new
            {
                server = NormalizeHost(node.address),
                server_port = node.port,
                password = node.id,
                sni = string.IsNullOrEmpty(sni) ? null : NormalizeHost(sni),
                verify_tls = !allowInsecure,
                socks_port = socksPort,
                http_port = httpPort,
                alpn = node.GetAlpn()
            }, new JsonSerializerSettings { NullValueHandling = NullValueHandling.Ignore });
        }

        private static bool EmptyOrEquals(string value, string expected)
        {
            return string.IsNullOrWhiteSpace(value)
                || string.Equals(value.Trim(), expected, StringComparison.OrdinalIgnoreCase);
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
