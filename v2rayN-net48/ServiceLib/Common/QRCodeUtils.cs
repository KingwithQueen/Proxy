using QRCoder;
using QRCoder.Exceptions;
using System.Drawing;
using ZXing;

namespace ServiceLib.Common;

public class QRCodeUtils
{
    public static byte[]? GenQRCode(string? url)
    {
        if (url.IsNullOrEmpty())
        {
            return null;
        }
        using QRCodeGenerator qrGenerator = new();
        DataTooLongException? lastDtle = null;

        var levels = new[]
        {
            QRCodeGenerator.ECCLevel.H,
            QRCodeGenerator.ECCLevel.Q,
            QRCodeGenerator.ECCLevel.M,
            QRCodeGenerator.ECCLevel.L
        };
        foreach (var level in levels)
        {
            try
            {
                using var qrCodeData = qrGenerator.CreateQrCode(url, level);
                using PngByteQRCode qrCode = new(qrCodeData);
                return qrCode.GetGraphic(20);
            }
            catch (DataTooLongException ex)
            {
                lastDtle = ex;
                continue;
            }
            catch
            {
                throw;
            }
        }

        if (lastDtle != null)
        {
            throw lastDtle;
        }

        return null;
    }

    public static string? ParseBarcode(string? fileName)
    {
        if (fileName == null || !File.Exists(fileName))
        {
            return null;
        }

        try
        {
            using var bitmap = new Bitmap(fileName);
            return ReadBarcode(bitmap);
        }
        catch
        {
            return null;
        }
    }

    public static string? ParseBarcode(byte[]? bytes)
    {
        if (bytes == null)
        {
            return null;
        }

        try
        {
            using var stream = new MemoryStream(bytes);
            using var bitmap = new Bitmap(stream);
            return ReadBarcode(bitmap);
        }
        catch
        {
            return null;
        }
    }

    private static string? ReadBarcode(Bitmap bitmap)
    {
        var reader = new BarcodeReader();
        var result = reader.Decode(bitmap);
        if (result?.Text.IsNotEmpty() == true)
        {
            return result.Text;
        }

        bitmap.RotateFlip(RotateFlipType.RotateNoneFlipX);
        return reader.Decode(bitmap)?.Text;
    }
}
