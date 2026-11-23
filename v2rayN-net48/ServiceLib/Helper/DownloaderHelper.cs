using System.Net.Http.Headers;
using System.Security.Authentication;

namespace ServiceLib.Helper;

public class DownloaderHelper
{
    public static DownloaderHelper Instance { get; } = new();

    internal static HttpClient CreateClient(IWebProxy? proxy, bool allowRedirect = true, IReadOnlyDictionary<string, string>? headers = null)
    {
        var handler = new HttpClientHandler
        {
            AllowAutoRedirect = allowRedirect,
            Proxy = proxy,
            UseProxy = proxy != null,
            AutomaticDecompression = DecompressionMethods.GZip | DecompressionMethods.Deflate,
            SslProtocols = SslProtocols.Tls12,
        };
        if (CertPemManager.Instance.HasCustomRootProvider)
        {
            handler.ServerCertificateCustomValidationCallback = (_, cert, chain, errors) =>
                CertPemManager.Instance.ValidateDownloadCertificate(cert, chain, errors);
        }

        return new HttpClient(HttpRequestHeadersHelper.CreateHandler(handler, headers))
        {
            Timeout = Timeout.InfiniteTimeSpan,
        };
    }

    public async Task<string?> DownloadStringAsync(IWebProxy? webProxy, string url, string? userAgent,
        IReadOnlyDictionary<string, string>? requestHeaders = null, string? acceptHeader = null, CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrEmpty(url))
        {
            return null;
        }

        var uri = new Uri(url);
        using var client = CreateClient(webProxy, headers: requestHeaders);
        client.DefaultRequestHeaders.UserAgent.TryParseAdd(userAgent ?? Utils.GetVersion(false));
        if (!string.IsNullOrWhiteSpace(acceptHeader))
        {
            client.DefaultRequestHeaders.Accept.ParseAdd(acceptHeader);
        }
        if (!string.IsNullOrEmpty(uri.UserInfo))
        {
            client.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Basic", Utils.Base64Encode(uri.UserInfo));
            uri = new UriBuilder(uri) { UserName = "", Password = "" }.Uri;
        }

        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(webProxy == null ? Global.DirectFetch : Global.ProxyFetch);
        using var response = await client.GetAsync(uri, timeout.Token).ConfigureAwait(false);
        response.EnsureSuccessStatusCode();
        return await response.Content.ReadAsStringAsync().ConfigureAwait(false);
    }

    public async Task DownloadDataAsync4Speed(IWebProxy webProxy, string url, Action<string> onProgress, CancellationToken cancellationToken = default)
    {
        using var client = CreateClient(webProxy);
        var interval = Stopwatch.StartNew();
        long lastBytes = 0;
        double peak = 0;
        long finalBytes = 0;
        await TransferAsync(client, url, Stream.Null, webProxy != null, (bytes, _) =>
        {
            finalBytes = bytes;
            if (interval.Elapsed.TotalSeconds < 1)
            {
                return;
            }

            peak = Math.Max(peak, (bytes - lastBytes) / interval.Elapsed.TotalSeconds);
            lastBytes = bytes;
            interval.Restart();
            onProgress((peak / 1_000_000).ToString("#0.0"));
        }, cancellationToken).ConfigureAwait(false);
        if (finalBytes > lastBytes && interval.Elapsed.TotalSeconds > 0)
        {
            peak = Math.Max(peak, (finalBytes - lastBytes) / interval.Elapsed.TotalSeconds);
        }
        onProgress((peak / 1_000_000).ToString("#0.0"));
    }

    public async Task DownloadFileAsync(IWebProxy? webProxy, FileDownloadRequest request, Action<FileDownloadState> onProgress, CancellationToken cancellationToken = default)
    {
        if (string.IsNullOrEmpty(request?.FilePath))
        {
            throw new ArgumentException("A destination path is required.", nameof(request));
        }

        var partial = request.FilePath + "." + Guid.NewGuid().ToString("N") + ".part";
        var state = new FileDownloadState { Request = request };
        var timer = Stopwatch.StartNew();
        using var client = CreateClient(webProxy);
        try
        {
            using (var output = new FileStream(partial, FileMode.CreateNew, FileAccess.Write, FileShare.None, 65536, true))
            {
                await TransferAsync(client, request.FileUrl, output, webProxy != null, (bytes, total) =>
                {
                    state = state with
                    {
                        DownloadedBytes = bytes,
                        TotalBytes = total,
                        SpeedBytesPerSecond = bytes / Math.Max(timer.Elapsed.TotalSeconds, 0.001),
                    };
                    onProgress(state);
                }, cancellationToken).ConfigureAwait(false);
            }

            cancellationToken.ThrowIfCancellationRequested();
            if (File.Exists(request.FilePath))
            {
                File.Replace(partial, request.FilePath, null);
            }
            else
            {
                File.Move(partial, request.FilePath);
            }
        }
        finally
        {
            File.Delete(partial);
        }
        onProgress(state with { Completed = true });
    }

    public Task DownloadSmallFilesAsync(IWebProxy? proxy, List<FileDownloadRequest> requests,
        Action<ReadOnlyMemory<FileDownloadState>> onProgress, CancellationToken token = default)
    {
        if (requests is not { Count: > 0 })
        {
            throw new ArgumentException("At least one download is required.", nameof(requests));
        }

        var states = requests.Select(request => new FileDownloadState { Request = request }).ToArray();
        void Update(int index, FileDownloadState state)
        {
            lock (states)
            {
                states[index] = state;
                onProgress(states);
            }
        }

        return FrameworkIO.ForEachAsync(Enumerable.Range(0, requests.Count),
            new ParallelOptions { MaxDegreeOfParallelism = 4, CancellationToken = token },
            async (index, cancellation) =>
            {
                try
                {
                    await DownloadFileAsync(proxy, requests[index], state => Update(index, state), cancellation).ConfigureAwait(false);
                }
                catch (Exception error) when (!cancellation.IsCancellationRequested)
                {
                    Update(index, states[index] with { Completed = true, Error = error });
                }
            });
    }

    private static async Task TransferAsync(HttpClient client, string url, Stream output, bool proxied,
        Action<long, long> onProgress, CancellationToken token)
    {
        using var connect = CancellationTokenSource.CreateLinkedTokenSource(token);
        connect.CancelAfter(proxied ? Global.ProxyDownloadConnect : Global.DirectDownloadConnect);
        using var response = await client.GetAsync(url, HttpCompletionOption.ResponseHeadersRead, connect.Token).ConfigureAwait(false);
        response.EnsureSuccessStatusCode();

        var total = response.Content.Headers.ContentLength ?? 0;
        using var input = await response.Content.ReadAsStreamAsync().ConfigureAwait(false);
        using var closeOnCancel = token.Register(input.Dispose);
        var buffer = new byte[65536];
        long received = 0;
        var progressTimer = Stopwatch.StartNew();
        onProgress(0, total);
        try
        {
            int count;
            while ((count = await input.ReadAsync(buffer, token).ConfigureAwait(false)) > 0)
            {
                token.ThrowIfCancellationRequested();
                await output.WriteAsync(buffer, 0, count, token).ConfigureAwait(false);
                received += count;
                if (progressTimer.ElapsedMilliseconds >= 150)
                {
                    onProgress(received, total);
                    progressTimer.Restart();
                }
            }
        }
        catch (Exception) when (token.IsCancellationRequested)
        {
            throw new OperationCanceledException(token);
        }
        token.ThrowIfCancellationRequested();
        onProgress(received, total);
    }
}
