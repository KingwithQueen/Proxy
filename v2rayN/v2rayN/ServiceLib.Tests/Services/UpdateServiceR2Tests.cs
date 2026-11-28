namespace ServiceLib.Tests.Services;

public class UpdateServiceR2Tests
{
    [Test]
    public async Task DefaultCoreType_ShouldUseJrayExceptTuicAndNaive()
    {
        foreach (var configType in Enum.GetValues<EConfigType>())
        {
            var expected = configType is EConfigType.TUIC or EConfigType.Naive
                ? ECoreType.sing_box
                : ECoreType.Jray;

            await Global.GetDefaultCoreType(configType).Should().BeEqualTo(expected);
        }

        await Global.CoreTypes.FirstOrDefault().Should().BeEqualTo(nameof(ECoreType.Jray));

        var config = new Config
        {
            CoreTypeItem =
            [
                new CoreTypeItem { ConfigType = EConfigType.Anytls, CoreType = ECoreType.Xray },
            ],
        };
        ConfigHandler.EnsureDefaultCoreTypes(config);
        await config.CoreTypeItem.First(t => t.ConfigType == EConfigType.VMess).CoreType.Should().BeEqualTo(ECoreType.Jray);
        await config.CoreTypeItem.First(t => t.ConfigType == EConfigType.VLESS).CoreType.Should().BeEqualTo(ECoreType.Jray);
        await config.CoreTypeItem.First(t => t.ConfigType == EConfigType.Anytls).CoreType.Should().BeEqualTo(ECoreType.Jray);
        await config.CoreTypeItem.First(t => t.ConfigType == EConfigType.TUIC).CoreType.Should().BeEqualTo(ECoreType.sing_box);
        await config.CoreTypeItem.First(t => t.ConfigType == EConfigType.Naive).CoreType.Should().BeEqualTo(ECoreType.sing_box);
    }

    [Test]
    public async Task CoreInfoManager_ShouldUseR2ForV2rayNAndJrayAndOfficialGithubForOthers()
    {
        var manager = CoreInfoManager.Instance;
        var checkTypes = manager.GetCheckUpdateCoreTypes();

        await checkTypes.Should().Contain(ECoreType.v2rayN);
        await checkTypes.Should().Contain(ECoreType.Jray);
        await checkTypes.Should().Contain(ECoreType.Xray);
        await checkTypes.Should().Contain(ECoreType.mihomo);
        await checkTypes.Should().Contain(ECoreType.sing_box);

        await manager.IsCheckUpdateSupported(ECoreType.Jray).Should().BeTrue();
        await manager.GetCheckPreRelease(ECoreType.Jray, true).Should().BeTrue();

        var v2rayNInfo = manager.GetCoreInfo(ECoreType.v2rayN)!;
        await v2rayNInfo.UseReleaseApiOnly.Should().BeTrue();
        await v2rayNInfo.ReleaseApiUrl!.StartsWith(Global.V2rayNReleaseUrl, StringComparison.Ordinal).Should().BeTrue();
        await v2rayNInfo.DownloadUrlWin64!.StartsWith(Global.V2rayNReleaseUrl, StringComparison.Ordinal).Should().BeTrue();
        await v2rayNInfo.DownloadUrlLinux64!.StartsWith(Global.V2rayNReleaseUrl, StringComparison.Ordinal).Should().BeTrue();

        var jrayInfo = manager.GetCoreInfo(ECoreType.Jray)!;
        await jrayInfo.UseReleaseApiOnly.Should().BeTrue();
        await jrayInfo.ReleaseApiUrl!.StartsWith(Global.JrayReleaseUrl, StringComparison.Ordinal).Should().BeTrue();
        await jrayInfo.DownloadUrlWin64!.StartsWith(Global.JrayReleaseUrl, StringComparison.Ordinal).Should().BeTrue();
        await jrayInfo.DownloadUrlLinux64!.StartsWith(Global.JrayReleaseUrl, StringComparison.Ordinal).Should().BeTrue();

        var xrayInfo = manager.GetCoreInfo(ECoreType.Xray)!;
        await xrayInfo.UseReleaseApiOnly.Should().BeFalse();
        await xrayInfo.Url!.StartsWith(Global.GithubUrl, StringComparison.Ordinal).Should().BeTrue();

        var mihomoInfo = manager.GetCoreInfo(ECoreType.mihomo)!;
        await mihomoInfo.UseReleaseApiOnly.Should().BeFalse();
        await mihomoInfo.Url!.StartsWith(Global.GithubUrl, StringComparison.Ordinal).Should().BeTrue();

        var singboxInfo = manager.GetCoreInfo(ECoreType.sing_box)!;
        await singboxInfo.UseReleaseApiOnly.Should().BeFalse();
        await singboxInfo.Url!.StartsWith(Global.GithubUrl, StringComparison.Ordinal).Should().BeTrue();
    }

    [Test]
    public async Task SelectRelease_ShouldRespectPrereleaseAndPickHighestVersion()
    {
        var releases = new List<GitHubRelease>
        {
            new() { TagName = "7.25.5", Prerelease = false, Draft = false },
            new() { TagName = "7.26.0-beta.1", Prerelease = true, Draft = false },
            new() { TagName = "7.25.6", Prerelease = false, Draft = false },
            new() { TagName = "7.27.0", Prerelease = false, Draft = true },
        };

        var stable = UpdateService.SelectRelease(releases, preRelease: false, lockedMaxVersion: null, pickHighestVersion: true);
        await stable.Should().NotBeNull();
        await stable!.TagName.Should().BeEqualTo("7.25.6");

        var pre = UpdateService.SelectRelease(releases, preRelease: true, lockedMaxVersion: null, pickHighestVersion: true);
        await pre.Should().NotBeNull();
        await pre!.TagName.Should().BeEqualTo("7.26.0-beta.1");
    }

    [Test]
    public async Task ResolveDownloadUrl_ShouldPreferMatchingReleaseAssetForR2()
    {
        var coreInfo = new CoreInfo
        {
            CoreType = ECoreType.Jray,
            UseReleaseApiOnly = true,
        };
        var release = new GitHubRelease
        {
            TagName = "26.11.9",
            Assets =
            [
                new GitHubReleaseAsset
                {
                    Name = "Jray-windows-64.zip",
                    BrowserDownloadUrl = "https://jray-release.jsnowly.com/Windows/26.11.9/custom-name.zip",
                },
            ],
        };
        var result = new UpdateResult(true, new SemanticVersion("26.11.9")) { Release = release };

        var resolved = UpdateService.ResolveDownloadUrl(
            ECoreType.Jray,
            coreInfo,
            result,
            "https://jray-release.jsnowly.com/Windows/{0}/Jray-windows-64.zip",
            result.Version!);

        await resolved.Should().BeEqualTo("https://jray-release.jsnowly.com/Windows/26.11.9/custom-name.zip");
    }
}
