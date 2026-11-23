using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using v2rayN.Handler;
using v2rayN.Mode;

namespace v2rayN.Forms
{
    public partial class MainForm
    {
        private CancellationTokenSource latencyCancellation;

        private async void FastRealPing_Click(object sender, EventArgs e)
        {
            if (latencyCancellation != null)
            {
                latencyCancellation.Cancel();
                return;
            }

            await RunRealLatencyAsync(lstVmess);
        }

        private async Task RunRealLatencyAsync(List<VmessItem> nodes)
        {
            if (appExiting || latencyCancellation != null || nodes == null || nodes.Count == 0)
            {
                return;
            }

            using (var cancellation = new CancellationTokenSource())
            {
                latencyCancellation = cancellation;
                var token = cancellation.Token;
                try
                {
                    RefreshLatencyCommand(true);
                    var progress = new Progress<KeyValuePair<string, string>>(result =>
                    {
                        if (!appExiting && !token.IsCancellationRequested)
                        {
                            ApplyLatencyResult(result.Key, result.Value);
                        }
                    });
                    await SpeedtestHandler.RunRealPingAsync(config, v2rayHandler, nodes, progress, token);
                }
                catch (OperationCanceledException) when (token.IsCancellationRequested)
                {
                }
                catch (Exception ex)
                {
                    Utils.SaveLog(ex.Message, ex);
                    if (!appExiting)
                    {
                        AppendText(false, ex.Message);
                    }
                }
                finally
                {
                    latencyCancellation = null;
                    if (!appExiting)
                    {
                        RefreshLatencyCommand(false);
                    }
                }
            }
        }

        private void ApplyLatencyResult(string indexId, string result)
        {
            config.GetVmessItem(indexId)?.SetTestResult(result);
            int index = lstVmess.FindIndex(node => node.indexId == indexId);
            if (index >= 0 && index < lvServers.Items.Count)
            {
                ShowProfileTestResult(lvServers.Items[index], result);
            }
        }
    }
}
