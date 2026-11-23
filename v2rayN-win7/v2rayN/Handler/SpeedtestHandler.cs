using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Linq;
using System.Net;
using System.Net.Sockets;
using System.Threading;
using System.Threading.Tasks;
using v2rayN.Mode;
using v2rayN.Resx;

namespace v2rayN.Handler
{
    class SpeedtestHandler
    {
        private Config _config;
        private V2rayHandler _v2rayHandler;
        private List<ServerTestItem> _selecteds;
        Action<string, string> _updateFunc;

        public SpeedtestHandler(Config config)
        {
            _config = config;
        }

        public SpeedtestHandler(Config config, V2rayHandler v2rayHandler, List<VmessItem> selecteds, ESpeedActionType actionType, Action<string, string> update)
        {
            _config = config;
            _v2rayHandler = v2rayHandler;
            //_selecteds = Utils.DeepCopy(selecteds);
            _updateFunc = update;

            _selecteds = selecteds.Select(CreateTestItem).ToList();

            if (actionType == ESpeedActionType.Ping)
            {
                Task.Run(RunPing);
            }
            else if (actionType == ESpeedActionType.Tcping)
            {
                Task.Run(RunTcping);
            }
            else if (actionType == ESpeedActionType.Speedtest)
            {
                Task.Run(RunSpeedTestAsync);
            }
        }

        private void RunPingSub(Action<ServerTestItem> updateFun)
        {
            try
            {
                foreach (var it in _selecteds.Where(it => it.configType != EConfigType.Custom))
                {
                    try
                    {
                        Task.Run(() => updateFun(it));
                    }
                    catch (Exception ex)
                    {
                        Utils.SaveLog(ex.Message, ex);
                    }
                }

                Thread.Sleep(10);
            }
            catch (Exception ex)
            {
                Utils.SaveLog(ex.Message, ex);
            }
        }


        private void RunPing()
        {
            RunPingSub((ServerTestItem it) =>
            {
                long time = Utils.Ping(it.address);

                _updateFunc(it.indexId, FormatOut(time, "ms"));
            });
        }

        private void RunTcping()
        {
            RunPingSub((ServerTestItem it) =>
            {
                int time = GetTcpingTime(it.address, it.port);

                _updateFunc(it.indexId, FormatOut(time, "ms"));
            });
        }

        public static Task RunRealPingAsync(Config config, V2rayHandler handler, IEnumerable<VmessItem> nodes,
            IProgress<KeyValuePair<string, string>> progress, CancellationToken cancellationToken)
        {
            var selecteds = nodes.Where(node => node.configType != EConfigType.Custom)
                .Select(CreateTestItem).ToList();
            if (selecteds.Count == 0)
            {
                return Task.CompletedTask;
            }

            var snapshot = Utils.DeepCopy(config);
            if (!Uri.TryCreate(snapshot.constItem.speedPingTestUrl, UriKind.Absolute, out var endpoint)
                || (endpoint.Scheme != Uri.UriSchemeHttp && endpoint.Scheme != Uri.UriSchemeHttps))
            {
                throw new ArgumentException(ResUI.CheckServerSettings);
            }

            string failed = ResUI.OperationFailed;
            string timedOut = ResUI.SpeedtestTimeout;
            return Task.Run(async () =>
            {
                const int batchSize = 8;
                for (int offset = 0; offset < selecteds.Count; offset += batchSize)
                {
                    cancellationToken.ThrowIfCancellationRequested();
                    var batch = selecteds.GetRange(offset, Math.Min(batchSize, selecteds.Count - offset));
                    var processes = handler.LoadSpeedTestProcesses(snapshot, batch, cancellationToken);
                    try
                    {
                        await Task.WhenAll(batch.Select(async item =>
                        {
                            var result = await MeasureRealPingAsync(endpoint, item, failed, timedOut, cancellationToken)
                                .ConfigureAwait(false);
                            cancellationToken.ThrowIfCancellationRequested();
                            progress.Report(new KeyValuePair<string, string>(item.indexId, result));
                        })).ConfigureAwait(false);
                    }
                    finally
                    {
                        processes.ForEach(V2rayHandler.StopProcess);
                    }
                }
            }, cancellationToken);
        }

        private static ServerTestItem CreateTestItem(VmessItem node)
        {
            return new ServerTestItem
            {
                indexId = node.indexId,
                address = node.address,
                port = node.port,
                configType = node.configType
            };
        }

        private static async Task<string> MeasureRealPingAsync(Uri endpoint, ServerTestItem item,
            string failed, string timedOut, CancellationToken cancellationToken)
        {
            cancellationToken.ThrowIfCancellationRequested();
            if (!item.allowTest)
            {
                return failed;
            }

            var request = (HttpWebRequest)WebRequest.Create(endpoint);
            request.Proxy = new WebProxy(Global.Loopback, item.port);
            request.AllowAutoRedirect = false;
            request.KeepAlive = false;
            using (var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken))
            {
                timeout.CancelAfter(TimeSpan.FromSeconds(8));
                using (timeout.Token.Register(request.Abort))
                {
                    var timer = Stopwatch.StartNew();
                    try
                    {
                        using (var response = (HttpWebResponse)await request.GetResponseAsync().ConfigureAwait(false))
                        {
                            return response.StatusCode == HttpStatusCode.OK || response.StatusCode == HttpStatusCode.NoContent
                                ? FormatOut(timer.ElapsedMilliseconds, "ms")
                                : failed;
                        }
                    }
                    catch (WebException ex)
                    {
                        ex.Response?.Close();
                        cancellationToken.ThrowIfCancellationRequested();
                        return timeout.IsCancellationRequested ? timedOut : failed;
                    }
                }
            }
        }

        private async Task RunSpeedTestAsync()
        {
            string testIndexId = string.Empty;
            var processes = _v2rayHandler.LoadSpeedTestProcesses(_config, _selecteds);

            try
            {
                string url = _config.constItem.speedTestUrl;
                DownloadHandle downloadHandle2 = new DownloadHandle();
                downloadHandle2.UpdateCompleted += (sender2, args) =>
                {
                    _config.GetVmessItem(testIndexId)?.SetTestResult(args.Msg);
                    _updateFunc(testIndexId, args.Msg);
                };
                downloadHandle2.Error += (sender2, args) =>
                {
                    _updateFunc(testIndexId, args.GetException().Message);
                };

                var timeout = 8;
                foreach (var it in _selecteds)
                {
                    if (it.configType == EConfigType.Custom)
                    {
                        continue;
                    }
                    if (!it.allowTest)
                    {
                        _updateFunc(it.indexId, ResUI.OperationFailed);
                        continue;
                    }
                    testIndexId = it.indexId;
                    if (_config.FindIndexId(it.indexId) < 0) continue;

                    WebProxy webProxy = new WebProxy(Global.Loopback, it.port);
                    await downloadHandle2.DownloadDataAsync(url, webProxy, timeout);
                }
            }
            finally
            {
                processes.ForEach(V2rayHandler.StopProcess);
            }
        }


        private int GetTcpingTime(string url, int port)
        {
            int responseTime = -1;

            try
            {
                if (!IPAddress.TryParse(url, out IPAddress ipAddress))
                {
                    IPHostEntry ipHostInfo = System.Net.Dns.GetHostEntry(url);
                    ipAddress = ipHostInfo.AddressList[0];
                }

                Stopwatch timer = new Stopwatch();
                timer.Start();

                IPEndPoint endPoint = new IPEndPoint(ipAddress, port);
                Socket clientSocket = new Socket(endPoint.AddressFamily, SocketType.Stream, ProtocolType.Tcp);

                IAsyncResult result = clientSocket.BeginConnect(endPoint, null, null);
                if (!result.AsyncWaitHandle.WaitOne(TimeSpan.FromSeconds(5)))
                    throw new TimeoutException("connect timeout (5s): " + url);
                clientSocket.EndConnect(result);

                timer.Stop();
                responseTime = timer.Elapsed.Milliseconds;
                clientSocket.Close();
            }
            catch (Exception ex)
            {
                Utils.SaveLog(ex.Message, ex);
            }
            return responseTime;
        }

        private static string FormatOut(object time, string unit)
        {
            if (time.ToString().Equals("-1"))
            {
                return "Timeout";
            }
            return $"{time}{unit}".PadLeft(8, ' ');
        }
    }
}
