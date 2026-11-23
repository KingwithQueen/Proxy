using System.Net.Sockets;

namespace Win7;

internal static class FrameworkIO
{
    public static Task<string> ReadAllTextAsync(string path) => Task.Run(() => File.ReadAllText(path));

    public static Task WriteAllTextAsync(string path, string text) => Task.Run(() => File.WriteAllText(path, text));

    public static Task AppendAllTextAsync(string path, string text) => Task.Run(() => File.AppendAllText(path, text));

    public static void Write(this Stream stream, byte[] buffer) => stream.Write(buffer, 0, buffer.Length);

    public static Task WriteAsync(this Stream stream, byte[] buffer, CancellationToken token) =>
        stream.WriteAsync(buffer, 0, buffer.Length, token);

    public static Task<int> ReadAsync(this Stream stream, byte[] buffer, CancellationToken token) =>
        stream.ReadAsync(buffer, 0, buffer.Length, token);

    public static async Task ReadExactlyAsync(this Stream stream, byte[] buffer, CancellationToken token)
    {
        var total = 0;
        while (total < buffer.Length)
        {
            var read = await stream.ReadAsync(buffer, total, buffer.Length - total, token).ConfigureAwait(false);
            if (read == 0)
            {
                throw new EndOfStreamException();
            }
            total += read;
        }
    }

    public static Task ConnectAsync(this TcpClient client, string address, int port, CancellationToken token) =>
        ConnectAsync(client, () => client.ConnectAsync(address, port), token);

    public static Task ConnectAsync(this TcpClient client, System.Net.IPAddress address, int port, CancellationToken token) =>
        ConnectAsync(client, () => client.ConnectAsync(address, port), token);

    private static async Task ConnectAsync(TcpClient client, Func<Task> connect, CancellationToken token)
    {
        token.ThrowIfCancellationRequested();
        using var cancellation = token.Register(client.Close);
        try
        {
            await connect().ConfigureAwait(false);
        }
        catch (Exception) when (token.IsCancellationRequested)
        {
            throw new OperationCanceledException(token);
        }
        token.ThrowIfCancellationRequested();
    }

    public static Task ForEachAsync<T>(IEnumerable<T> items, ParallelOptions options, Func<T, CancellationToken, Task> action)
    {
        return Task.WhenAll(Partitioner.Create(items).GetPartitions(Math.Max(1, options.MaxDegreeOfParallelism))
            .Select(async partition =>
            {
                using (partition)
                {
                    while (partition.MoveNext())
                    {
                        options.CancellationToken.ThrowIfCancellationRequested();
                        await action(partition.Current, options.CancellationToken).ConfigureAwait(false);
                    }
                }
            }));
    }
}
