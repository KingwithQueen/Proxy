using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using v2rayN.Mode;
using v2rayN.Resx;

namespace v2rayN.Handler
{

    /// <summary>
    /// 消息委托
    /// </summary>
    /// <param name="notify">是否显示在托盘区</param>
    /// <param name="msg">内容</param>
    public delegate void ProcessDelegate(bool notify, string msg);

    /// <summary>
    /// v2ray进程处理类
    /// </summary>
    class V2rayHandler
    {
        private static string v2rayConfigRes = Global.v2rayConfigFileName;
        private CoreInfo coreInfo;
        public event ProcessDelegate ProcessEvent;
        public event Action ActiveCoreExited;
        private readonly object coreLock = new object();
        private Process customSocksProcess;
        private volatile Process _process;
        private bool shuttingDown;

        public bool IsRunning
        {
            get
            {
                var process = _process;
                try
                {
                    return process != null && !process.HasExited;
                }
                catch (InvalidOperationException)
                {
                    return false;
                }
            }
        }

        public V2rayHandler()
        {
        }

        /// <summary>
        /// 载入V2ray
        /// </summary>
        public void LoadV2ray(Config config)
        {
            lock (coreLock)
            {
                if (shuttingDown || !Global.reloadV2ray)
                {
                    return;
                }

                Global.reloadV2ray = false;
                var item = ConfigHandler.GetDefaultServer(ref config);
                if (item == null)
                {
                    V2rayStop();
                    ShowMsg(false, ResUI.CheckServerSettings);
                    return;
                }

                var coreType = LazyConfig.Instance.GetCoreType(item, item.configType);
                if (coreType == ECoreType.Cray)
                {
                    V2rayStop();
                    coreInfo = null;
                    ShowMsg(true, $"[{config.GetGroupRemarks(item.groupId)}] {item.GetSummary()}");
                    _process = StartCray(config, item,
                        config.GetLocalPort(Global.InboundSocks), config.GetLocalPort(Global.InboundHttp));
                    return;
                }

                if (SetCore(coreType) != 0)
                {
                    ShowMsg(false, ResUI.CheckServerSettings);
                    return;
                }
                string fileName = Utils.GetPath(v2rayConfigRes);
                if (V2rayConfigHandler.GenerateClientConfig(item, fileName, out string msg, out string content) != 0)
                {
                    ShowMsg(false, msg);
                }
                else
                {
                    ShowMsg(false, msg);
                    ShowMsg(true, $"[{config.GetGroupRemarks(item.groupId)}] {item.GetSummary()}");
                    V2rayRestart();
                }

                //start a socks service
                if (_process != null && !_process.HasExited && item.configType == EConfigType.Custom && item.preSocksPort > 0)
                {
                    var itemSocks = new VmessItem()
                    {
                        configType = EConfigType.Socks,
                        address = Global.Loopback,
                        port = item.preSocksPort
                    };
                    if (V2rayConfigHandler.GenerateClientConfig(itemSocks, null, out string msg2, out string configStr) == 0)
                    {
                        customSocksProcess = V2rayStartNew(configStr);
                    }
                }
            }
        }

        /// <summary>
        /// 新建进程，载入V2ray配置文件字符串
        /// 返回已监听测速端口的进程。
        /// </summary>
        public List<Process> LoadSpeedTestProcesses(Config config, List<ServerTestItem> selecteds)
        {
            return LoadSpeedTestProcesses(config, selecteds, CancellationToken.None);
        }

        internal List<Process> LoadSpeedTestProcesses(Config config, List<ServerTestItem> selecteds,
            CancellationToken cancellationToken)
        {
            var processes = new List<Process>();
            try
            {
                StartSpeedTestProcesses(config, selecteds, processes, cancellationToken);
                return processes;
            }
            catch
            {
                processes.ForEach(StopProcess);
                throw;
            }
        }

        private void StartSpeedTestProcesses(Config config, List<ServerTestItem> selecteds, List<Process> processes,
            CancellationToken cancellationToken)
        {
            var legacyNodes = new List<ServerTestItem>();
            foreach (var item in selecteds)
            {
                cancellationToken.ThrowIfCancellationRequested();
                var node = config.GetVmessItem(item.indexId);
                if (node == null || item.configType == EConfigType.Custom)
                {
                    continue;
                }
                if (LazyConfig.ResolveCoreType(config, node, node.configType) != ECoreType.Cray)
                {
                    legacyNodes.Add(item);
                    continue;
                }
                try
                {
                    var listener = new TcpListener(IPAddress.Loopback, 0);
                    try
                    {
                        listener.Start();
                        item.port = ((IPEndPoint)listener.LocalEndpoint).Port;
                    }
                    finally
                    {
                        listener.Stop();
                    }
                    var process = StartCray(config, node, 0, item.port, cancellationToken);
                    if (process != null)
                    {
                        processes.Add(process);
                        item.allowTest = true;
                    }
                }
                catch (SocketException ex)
                {
                    ShowMsg(false, ex.Message);
                }
            }

            if (legacyNodes.Count > 0)
            {
                cancellationToken.ThrowIfCancellationRequested();
                string configStr = V2rayConfigHandler.GenerateClientSpeedtestConfigString(config, legacyNodes, out string msg);
                ShowMsg(false, msg);
                var process = string.IsNullOrEmpty(configStr) ? null : V2rayStartNew(configStr);
                if (process != null)
                {
                    processes.Add(process);
                }
                else
                {
                    legacyNodes.ForEach(node => node.allowTest = false);
                }
            }
        }

        /// <summary>
        /// V2ray重启
        /// </summary>
        private void V2rayRestart()
        {
            V2rayStop();
            V2rayStart();
        }

        public void Shutdown()
        {
            lock (coreLock)
            {
                shuttingDown = true;
                V2rayStop();
            }
        }

        /// <summary>
        /// V2ray停止
        /// </summary>
        public void V2rayStop()
        {
            lock (coreLock)
            {
                var process = _process;
                _process = null;
                StopProcess(process);
                StopProcess(customSocksProcess);
                customSocksProcess = null;

                if (process == null && coreInfo?.coreExes != null)
                {
                    foreach (string vName in coreInfo.coreExes)
                    {
                        foreach (var existing in Process.GetProcessesByName(vName))
                        {
                            using (existing)
                            {
                                try
                                {
                                    if (existing.MainModule.FileName == $"{Utils.GetPath(vName)}.exe")
                                    {
                                        KillProcess(existing);
                                    }
                                }
                                catch (Exception ex)
                                {
                                    Utils.SaveLog(ex.Message, ex);
                                }
                            }
                        }
                    }
                }
            }
        }
        /// <summary>
        /// V2ray停止
        /// </summary>
        public static void StopProcess(Process process)
        {
            if (process != null)
            {
                using (process)
                {
                    KillProcess(process);
                }
            }
        }

        private string V2rayFindexe(List<string> lstCoreTemp)
        {
            string fileName = string.Empty;
            foreach (string name in lstCoreTemp)
            {
                string vName = $"{name}.exe";
                vName = Utils.GetPath(vName);
                if (File.Exists(vName))
                {
                    fileName = vName;
                    break;
                }
            }
            if (Utils.IsNullOrEmpty(fileName))
            {
                string msg = string.Format(ResUI.NotFoundCore, string.Join(", ", lstCoreTemp.ToArray()), coreInfo?.coreUrl ?? Global.xrayCoreUrl);
                ShowMsg(false, msg);
            }
            return fileName;
        }

        /// <summary>
        /// V2ray启动
        /// </summary>
        private void V2rayStart()
        {
            ShowMsg(false, string.Format(ResUI.StartService, DateTime.Now.ToString()));

            try
            {
                string fileName = V2rayFindexe(coreInfo.coreExes);
                if (fileName == "") return;

                Process p = new Process
                {
                    StartInfo = new ProcessStartInfo
                    {
                        FileName = fileName,
                        Arguments = coreInfo.arguments,
                        WorkingDirectory = Utils.StartupPath(),
                        UseShellExecute = false,
                        RedirectStandardOutput = coreInfo.redirectInfo,
                        RedirectStandardError = coreInfo.redirectInfo,
                        CreateNoWindow = true,
                        StandardOutputEncoding = coreInfo.redirectInfo ? Encoding.UTF8 : null,
                        StandardErrorEncoding = coreInfo.redirectInfo ? Encoding.UTF8 : null,
                    }
                };
                if (coreInfo.redirectInfo)
                {
                    p.OutputDataReceived += (sender, e) =>
                    {
                        if (!String.IsNullOrEmpty(e.Data))
                        {
                            string msg = e.Data + Environment.NewLine;
                            ShowMsg(false, msg);
                        }
                    };
                }
                p.Start();
                if (coreInfo.redirectInfo)
                {
                    p.BeginOutputReadLine();
                }
                _process = p;

                if (p.WaitForExit(1000))
                {
                    throw new Exception(coreInfo.redirectInfo ? p.StandardError.ReadToEnd() : "启动进程失败并退出 (Failed to start the process and exited)");
                }

                Global.processJob.AddProcess(p.Handle);
            }
            catch (Exception ex)
            {
                Utils.SaveLog(ex.Message, ex);
                string msg = ex.Message;
                ShowMsg(true, msg);
            }
        }
        /// <summary>
        /// V2ray启动，新建进程，传入配置字符串
        /// </summary>
        private Process V2rayStartNew(string configStr)
        {
            ShowMsg(false, string.Format(ResUI.StartService, DateTime.Now.ToString()));

            try
            {
                string fileName = V2rayFindexe(new List<string> { "xray", "wv2ray", "v2ray" });
                if (fileName == "") return null;

                Process p = new Process
                {
                    StartInfo = new ProcessStartInfo
                    {
                        FileName = fileName,
                        Arguments = "-config stdin:",
                        WorkingDirectory = Utils.StartupPath(),
                        UseShellExecute = false,
                        RedirectStandardInput = true,
                        RedirectStandardOutput = true,
                        RedirectStandardError = true,
                        CreateNoWindow = true,
                        StandardOutputEncoding = Encoding.UTF8,
                        StandardErrorEncoding = Encoding.UTF8
                    }
                };
                p.OutputDataReceived += (sender, e) =>
                {
                    if (!String.IsNullOrEmpty(e.Data))
                    {
                        string msg = e.Data + Environment.NewLine;
                        ShowMsg(false, msg);
                    }
                };
                p.Start();
                p.BeginOutputReadLine();

                p.StandardInput.Write(configStr);
                p.StandardInput.Close();

                if (p.WaitForExit(1000))
                {
                    throw new Exception(p.StandardError.ReadToEnd());
                }

                Global.processJob.AddProcess(p.Handle);
                return p;
            }
            catch (Exception ex)
            {
                Utils.SaveLog(ex.Message, ex);
                string msg = ex.Message;
                ShowMsg(false, msg);
                return null;
            }
        }

        private Process StartCray(Config config, VmessItem node, int socksPort, int httpPort,
            CancellationToken cancellationToken = default)
        {
            Process process = null;
            bool started = false;
            try
            {
                string path = Utils.GetPath("Cray.exe");
                if (!File.Exists(path))
                {
                    throw new FileNotFoundException(ResUI.CrayMissing);
                }

                byte[] payload = Encoding.UTF8.GetBytes(CrayConfig.Serialize(config, node, socksPort, httpPort));
                var ready = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
                process = new Process
                {
                    StartInfo = new ProcessStartInfo
                    {
                        FileName = path,
                        Arguments = "-config -",
                        WorkingDirectory = Utils.StartupPath(),
                        UseShellExecute = false,
                        CreateNoWindow = true,
                        RedirectStandardInput = true,
                        RedirectStandardOutput = true,
                        RedirectStandardError = true,
                        StandardOutputEncoding = Encoding.UTF8,
                        StandardErrorEncoding = Encoding.UTF8
                    },
                    EnableRaisingEvents = true
                };
                DataReceivedEventHandler log = (sender, e) =>
                {
                    if (e.Data == "Cray: ready")
                    {
                        ready.TrySetResult(true);
                    }
                    if (!string.IsNullOrEmpty(e.Data))
                    {
                        ShowMsg(false, e.Data);
                    }
                };
                process.OutputDataReceived += log;
                process.ErrorDataReceived += log;
                process.Exited += (sender, e) =>
                {
                    ready.TrySetResult(false);
                    if (ReferenceEquals(_process, sender))
                    {
                        ShowMsg(true, ResUI.CrayExited);
                        ActiveCoreExited?.Invoke();
                    }
                };

                cancellationToken.ThrowIfCancellationRequested();
                process.Start();
                started = true;
                Global.processJob?.AddProcess(process.Handle);
                process.BeginOutputReadLine();
                process.BeginErrorReadLine();
                using (var input = process.StandardInput)
                {
                    // Write UTF-8 bytes without using the system console code page.
                    input.BaseStream.Write(payload, 0, payload.Length);
                }
                if (!ready.Task.Wait(10000, cancellationToken) || !ready.Task.Result || process.HasExited)
                {
                    if (process.HasExited)
                    {
                        process.WaitForExit();
                    }
                    throw new InvalidOperationException(ResUI.CrayStartup);
                }
                return process;
            }
            catch (Exception ex)
            {
                if (started)
                {
                    StopProcess(process);
                }
                else
                {
                    process?.Dispose();
                }
                cancellationToken.ThrowIfCancellationRequested();
                Utils.SaveLog(ex.Message, ex);
                ShowMsg(false, ex.Message);
                return null;
            }
        }

        /// <summary>
        /// 消息委托
        /// </summary>
        /// <param name="updateToTrayTooltip">是否更新托盘图标的工具提示</param>
        /// <param name="msg">输出到日志框</param>
        private void ShowMsg(bool updateToTrayTooltip, string msg)
        {
            ProcessEvent?.Invoke(updateToTrayTooltip, msg);
        }

        private static void KillProcess(Process p)
        {
            try
            {
                p.CloseMainWindow();
                p.WaitForExit(100);
                if (!p.HasExited)
                {
                    p.Kill();
                    p.WaitForExit(2000);
                }
            }
            catch (Exception ex)
            {
                Utils.SaveLog(ex.Message, ex);
            }
        }

        private int SetCore(ECoreType coreType)
        {
            coreInfo = LazyConfig.Instance.GetCoreInfo(coreType);

            if (coreInfo == null)
            {
                return -1;
            }
            return 0;
        }
    }
}
