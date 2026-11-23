using System.Formats.Asn1;
using System.Security.Authentication;
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;

namespace ServiceLib.Manager;

/// <summary>
///     Manager for certificate operations with CA pinning to prevent MITM attacks
/// </summary>
public class CertPemManager
{
    private static readonly string _tag = "CertPemManager";
    private static readonly Lazy<CertPemManager> _instance = new(() => new());
    private Config _config;

    public async Task Init(Config config)
    {
        if (_config != null)
        {
            return;
        }
        _config = config;
        await Task.CompletedTask;
    }

    public static CertPemManager Instance => _instance.Value;

    /// <summary>
    ///     Get certificate in PEM format from a server with CA pinning validation
    /// </summary>
    public async Task<(string?, string?)> GetCertPemAsync(string target, string serverName,
        List<string>? verifyPeerCertByName = null)
    {
        try
        {
            var (domain, _, port, _) = Utils.ParseUrl(target);

            using var cts = new CancellationTokenSource(Global.LocalFetch);

            using var client = new TcpClient();
            await client.ConnectAsync(domain, port > 0 ? port : 443, cts.Token);

            var callback = new RemoteCertificateValidationCallback((sender, certificate, chain, sslPolicyErrors) =>
                ValidateServerCertificate(sender, certificate, chain, sslPolicyErrors, verifyPeerCertByName ?? []));
            using var ssl = new SslStream(client.GetStream(), false, callback);
            using var closeOnTimeout = cts.Token.Register(client.Close);
            try
            {
                await ssl.AuthenticateAsClientAsync(serverName, null, SslProtocols.Tls12, true);
            }
            catch (Exception) when (cts.IsCancellationRequested)
            {
                throw new OperationCanceledException(cts.Token);
            }

            var remote = ssl.RemoteCertificate;
            if (remote == null)
            {
                return (null, null);
            }

            using var leaf = new X509Certificate2(remote);
            return (ExportCertToPem(leaf), null);
        }
        catch (OperationCanceledException)
        {
            Logging.SaveLog(_tag, new TimeoutException($"Connection timeout after {Global.LocalFetch.TotalSeconds} seconds"));
            return (null, $"Connection timeout after {Global.LocalFetch.TotalSeconds} seconds");
        }
        catch (Exception ex)
        {
            Logging.SaveLog(_tag, ex);
            return (null, ex.Message);
        }
    }

    /// <summary>
    ///     Get certificate chain in PEM format from a server with CA pinning validation
    /// </summary>
    public async Task<(List<string>, string?)> GetCertChainPemAsync(string target, string serverName,
        List<string>? verifyPeerCertByName = null)
    {
        var pemList = new List<string>();
        try
        {
            var (domain, _, port, _) = Utils.ParseUrl(target);

            using var cts = new CancellationTokenSource(Global.LocalFetch);

            using var client = new TcpClient();
            await client.ConnectAsync(domain, port > 0 ? port : 443, cts.Token);

            var callback = new RemoteCertificateValidationCallback((sender, certificate, chain, sslPolicyErrors) =>
                ValidateServerCertificate(sender, certificate, chain, sslPolicyErrors, verifyPeerCertByName ?? []));
            using var ssl = new SslStream(client.GetStream(), false, callback);
            using var closeOnTimeout = cts.Token.Register(client.Close);
            try
            {
                await ssl.AuthenticateAsClientAsync(serverName, null, SslProtocols.Tls12, true);
            }
            catch (Exception) when (cts.IsCancellationRequested)
            {
                throw new OperationCanceledException(cts.Token);
            }

            if (ssl.RemoteCertificate is not X509Certificate2 certChain)
            {
                return (pemList, null);
            }

            using var chain = new X509Chain();
            chain.Build(certChain);

            pemList.AddRange(chain.ChainElements.Cast<X509ChainElement>().Select(element => ExportCertToPem(element.Certificate)));

            return (pemList, null);
        }
        catch (OperationCanceledException)
        {
            Logging.SaveLog(_tag, new TimeoutException($"Connection timeout after {Global.LocalFetch.TotalSeconds} seconds"));
            return (pemList, $"Connection timeout after {Global.LocalFetch.TotalSeconds} seconds");
        }
        catch (Exception ex)
        {
            Logging.SaveLog(_tag, ex);
            return (pemList, ex.Message);
        }
    }

    /// <summary>
    ///     Validate server certificate with CA pinning
    /// </summary>
    private bool ValidateServerCertificate(
        object _,
        X509Certificate? certificate,
        X509Chain? chain,
        SslPolicyErrors sslPolicyErrors,
        List<string> verifyPeerCertByName)
    {
        if (certificate == null || sslPolicyErrors.HasFlag(SslPolicyErrors.RemoteCertificateNotAvailable))
        {
            return false;
        }

        using var leaf = new X509Certificate2(certificate);
        if (!ValidateTrust(leaf, chain, sslPolicyErrors))
        {
            return false;
        }

        if (!sslPolicyErrors.HasFlag(SslPolicyErrors.RemoteCertificateNameMismatch))
        {
            return true;
        }

        var san = leaf.Extensions["2.5.29.17"];
        if (san == null || verifyPeerCertByName.Count == 0)
        {
            return false;
        }
        try
        {
            var names = new AsnReader(san.RawData, AsnEncodingRules.DER).ReadSequence();
            var dnsTag = new Asn1Tag(TagClass.ContextSpecific, 2);
            while (names.HasData)
            {
                if (names.PeekTag().HasSameClassAndValue(dnsTag))
                {
                    var name = names.ReadCharacterString(UniversalTagNumber.IA5String, dnsTag);
                    if (verifyPeerCertByName.Contains(name, StringComparer.OrdinalIgnoreCase))
                    {
                        return true;
                    }
                }
                else
                {
                    names.ReadEncodedValue();
                }
            }
        }
        catch (AsnContentException)
        {
            return false;
        }

        return false;
    }

    private bool ValidateTrust(X509Certificate2 leaf, X509Chain? suppliedChain, SslPolicyErrors errors)
    {
        if (!HasCustomRootProvider)
        {
            return !errors.HasFlag(SslPolicyErrors.RemoteCertificateChainErrors);
        }

        using var chain = new X509Chain();
        chain.ChainPolicy.RevocationMode = X509RevocationMode.NoCheck;
        chain.ChainPolicy.VerificationFlags = X509VerificationFlags.AllowUnknownCertificateAuthority;
        chain.ChainPolicy.ApplicationPolicy.Add(new Oid("1.3.6.1.5.5.7.3.1"));
        var roots = BuildTrustedCertificateCollection();
        chain.ChainPolicy.ExtraStore.AddRange(roots);
        if (suppliedChain != null)
        {
            foreach (X509ChainElement element in suppliedChain.ChainElements)
            {
                chain.ChainPolicy.ExtraStore.Add(element.Certificate);
            }
        }

        return chain.Build(leaf) &&
               chain.ChainStatus.All(status => (status.Status & ~X509ChainStatusFlags.UntrustedRoot) == 0) &&
               chain.ChainElements.Count > 0 &&
               roots.Contains(chain.ChainElements[chain.ChainElements.Count - 1].Certificate);
    }

    internal bool ValidateDownloadCertificate(X509Certificate2? certificate, X509Chain? chain, SslPolicyErrors errors) =>
        certificate != null && (errors & ~SslPolicyErrors.RemoteCertificateChainErrors) == 0 &&
        ValidateTrust(certificate, chain, errors);

    public static string ExportCertToPem(X509Certificate2 cert)
    {
        var der = cert.Export(X509ContentType.Cert);
        var b64 = Convert.ToBase64String(der);
        return $"-----BEGIN CERTIFICATE-----\n{b64}\n-----END CERTIFICATE-----\n";
    }

    /// <summary>
    ///     Parse concatenated PEM certificates string into a list of individual certificates
    ///     Normalizes format: removes line breaks from base64 content for better compatibility
    /// </summary>
    /// <param name="pemChain">Concatenated PEM certificates string (supports both \r\n and \n line endings)</param>
    /// <returns>List of individual PEM certificate strings with normalized format</returns>
    public static List<string> ParsePemChain(string pemChain)
    {
        var certs = new List<string>();
        if (string.IsNullOrWhiteSpace(pemChain))
        {
            return certs;
        }

        // Normalize line endings (CRLF -> LF) at the beginning
        pemChain = pemChain.Replace("\r\n", "\n").Replace("\r", "\n");

        const string beginMarker = "-----BEGIN CERTIFICATE-----";
        const string endMarker = "-----END CERTIFICATE-----";

        var index = 0;
        while (index < pemChain.Length)
        {
            var beginIndex = pemChain.IndexOf(beginMarker, index, StringComparison.Ordinal);
            if (beginIndex == -1)
            {
                break;
            }

            var endIndex = pemChain.IndexOf(endMarker, beginIndex, StringComparison.Ordinal);
            if (endIndex == -1)
            {
                break;
            }

            // Extract certificate content
            var base64Start = beginIndex + beginMarker.Length;
            var base64Content = pemChain.Substring(base64Start, endIndex - base64Start);

            // Remove all whitespace from base64 content
            base64Content = new string(base64Content.Where(c => !char.IsWhiteSpace(c)).ToArray());

            // Reconstruct with clean format: BEGIN marker + base64 (no line breaks) + END marker
            var normalizedCert = $"{beginMarker}\n{base64Content}\n{endMarker}\n";
            certs.Add(normalizedCert);

            // Move to next certificate
            index = endIndex + endMarker.Length;
        }

        return certs;
    }

    /// <summary>
    ///     Concatenate a list of PEM certificates into a single string
    /// </summary>
    /// <param name="pemList">List of individual PEM certificate strings</param>
    /// <returns>Concatenated PEM certificates string</returns>
    public static string ConcatenatePemChain(IEnumerable<string>? pemList)
    {
        return pemList == null ? string.Empty : string.Concat(pemList);
    }

    public static string GetCertSha256Thumbprint(string pemCert, bool includeColon = false)
    {
        try
        {
            using var cert = LoadPem(pemCert);
            return GetSha256(cert, includeColon);
        }
        catch
        {
            return string.Empty;
        }
    }

    public static string GetLeafCertSha256Thumbprint(string pemCertChain, bool includeColon = false)
    {
        var certs = ParsePemChain(pemCertChain);
        if (certs.Count == 0)
        {
            return string.Empty;
        }
        foreach (var certStr in certs)
        {
            try
            {
                using var cert = LoadPem(certStr);
                var extension = cert.Extensions
                    .OfType<X509BasicConstraintsExtension>()
                    .FirstOrDefault();
                var isCa = extension?.CertificateAuthority == true;
                if (isCa)
                {
                    continue;
                }
                return GetSha256(cert, includeColon);
            }
            catch
            {
                // Ignore
            }
        }
        return string.Empty;
    }

    private static X509Certificate2 LoadPem(string pem) =>
        new(Convert.FromBase64String(ParsePemChain(pem).First().Split('\n')[1]));

    private static string GetSha256(X509Certificate2 certificate, bool includeColon)
    {
        using var sha = SHA256.Create();
        var hex = BitConverter.ToString(sha.ComputeHash(certificate.RawData));
        return includeColon ? hex.Replace('-', ':') : hex.Replace("-", "");
    }

    private static X509Certificate2Collection LoadRoots(string resource)
    {
        var roots = new X509Certificate2Collection();
        foreach (var pem in ParsePemChain(EmbedUtils.GetEmbedText(resource)))
        {
            roots.Add(LoadPem(pem));
        }
        return roots;
    }

    private static readonly Lazy<X509Certificate2Collection> _chromeRootCerts =
        new(() => LoadRoots(Global.ChromeRootCertFileName));

    private static readonly Lazy<X509Certificate2Collection> _mozillaRootCerts =
        new(() => LoadRoots(Global.MozillaRootCertFileName));

    private X509Certificate2Collection BuildTrustedCertificateCollection() =>
        _config.GuiItem.RootCertProvider == Global.ChromeRootProvider ? _chromeRootCerts.Value : _mozillaRootCerts.Value;

    internal bool HasCustomRootProvider =>
        _config?.GuiItem.RootCertProvider is Global.ChromeRootProvider or Global.MozillaRootProvider;
}
