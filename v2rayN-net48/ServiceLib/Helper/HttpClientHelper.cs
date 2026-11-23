using System.Net.Http.Headers;

namespace ServiceLib.Helper;

/// <summary>
/// </summary>
public class HttpClientHelper
{
    private static readonly Lazy<HttpClientHelper> _instance = new(() =>
    {
        HttpClientHandler handler = new() { UseCookies = false, UseProxy = false };
        HttpClientHelper helper = new(new HttpClient(handler));
        return helper;
    });

    public static HttpClientHelper Instance => _instance.Value;
    private readonly HttpClient httpClient;

    private HttpClientHelper(HttpClient httpClient)
    {
        this.httpClient = httpClient;
    }

    public async Task<string?> TryGetAsync(string url, CancellationToken cancellationToken = default)
    {
        if (url.IsNullOrEmpty())
        {
            return null;
        }

        try
        {
            using var response = await httpClient.GetAsync(url, cancellationToken);
            return await response.Content.ReadAsStringAsync();
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            throw;
        }
        catch
        {
            return null;
        }
    }

    public async Task<string?> GetAsync(string url)
    {
        if (url.IsNullOrEmpty())
        {
            return null;
        }
        return await httpClient.GetStringAsync(url);
    }

    public async Task PutAsync(string url, Dictionary<string, string> headers)
    {
        var jsonContent = JsonUtils.Serialize(headers);
        using var content = new StringContent(jsonContent, Encoding.UTF8, "application/json");
        using var response = await httpClient.PutAsync(url, content);
    }

    public async Task PatchAsync(string url, Dictionary<string, string> headers)
    {
        var myContent = JsonUtils.Serialize(headers);
        var buffer = Encoding.UTF8.GetBytes(myContent);
        using var request = new HttpRequestMessage(new HttpMethod("PATCH"), url);
        request.Content = new ByteArrayContent(buffer);
        request.Content.Headers.ContentType = new MediaTypeHeaderValue("application/json");
        using var response = await httpClient.SendAsync(request);
    }

    public async Task DeleteAsync(string url)
    {
        await httpClient.DeleteAsync(url);
    }
}
