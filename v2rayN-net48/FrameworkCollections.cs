namespace Win7;

internal static class FrameworkCollections
{
    public static bool StartsWith(this string text, char value) => text.Length > 0 && text[0] == value;
    public static bool EndsWith(this string text, char value) => text.Length > 0 && text[text.Length - 1] == value;
    public static bool Contains(this string text, char value) => text.IndexOf(value) >= 0;

    public static string[] Split(this string text, char separator, int count) => text.Split(new[] { separator }, count);
    public static string[] Split(this string text, char separator, StringSplitOptions options) => text.Split(new[] { separator }, options);
    public static string[] Split(this string text, string separator, int count = int.MaxValue, StringSplitOptions options = StringSplitOptions.None) =>
        text.Split(new[] { separator }, count, options);
    public static string[] Split(this string text, string separator, StringSplitOptions options) =>
        text.Split(new[] { separator }, options);

    public static TValue GetValueOrDefault<TKey, TValue>(this IReadOnlyDictionary<TKey, TValue> dictionary, TKey key) =>
        dictionary.GetValueOrDefault(key, default!);

    public static TValue GetValueOrDefault<TKey, TValue>(this IReadOnlyDictionary<TKey, TValue> dictionary, TKey key, TValue fallback) =>
        dictionary.TryGetValue(key, out var value) ? value : fallback;

    public static bool TryAdd<TKey, TValue>(this IDictionary<TKey, TValue> dictionary, TKey key, TValue value)
    {
        if (dictionary.ContainsKey(key))
        {
            return false;
        }

        dictionary.Add(key, value);
        return true;
    }

    public static void Deconstruct<TKey, TValue>(this KeyValuePair<TKey, TValue> pair, out TKey key, out TValue value)
    {
        key = pair.Key;
        value = pair.Value;
    }

    public static T FirstOrDefault<T>(this IEnumerable<T> source, Func<T, bool> predicate, T fallback) =>
        source.Where(predicate).DefaultIfEmpty(fallback).First();

    public static T FirstOrDefault<T>(this IEnumerable<T> source, T fallback) =>
        source.DefaultIfEmpty(fallback).First();
}

internal sealed class PeriodicTimer(TimeSpan period) : IDisposable
{
    public async Task<bool> WaitForNextTickAsync(CancellationToken token = default)
    {
        await Task.Delay(period, token).ConfigureAwait(false);
        return true;
    }

    public void Dispose()
    {
    }
}
