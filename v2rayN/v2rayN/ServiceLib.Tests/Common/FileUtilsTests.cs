using ServiceLib.Common;

namespace ServiceLib.Tests.Common;

public class FileUtilsTests
{
    [Test]
    public async Task CreateLinuxShellFile_ShouldNormalizeCrlfAndRepairLegacyCrlfFile()
    {
        var fileName = $"test_shell_{Guid.NewGuid():N}.sh";
        var shPath = Utils.GetBinConfigPath(fileName);
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(shPath)!);
            await File.WriteAllTextAsync(shPath, "#!/bin/bash\r\necho old\r\n");

            var returnedPath = await FileUtils.CreateLinuxShellFile(fileName, "#!/bin/bash\r\necho fixed\r\n", overwrite: false);
            var written = await File.ReadAllTextAsync(returnedPath);

            await returnedPath.Should().BeEqualTo(shPath);
            await written.Contains('\r').Should().BeFalse();
            await written.Should().BeEqualTo("#!/bin/bash\necho fixed\n");
        }
        finally
        {
            if (File.Exists(shPath))
            {
                File.Delete(shPath);
            }
        }
    }
}
