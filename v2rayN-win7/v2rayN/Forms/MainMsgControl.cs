using System;
using System.ComponentModel;
using System.Drawing;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.RegularExpressions;
using System.Windows.Forms;
using v2rayN.Base;
using v2rayN.Mode;
using v2rayN.Resx;

namespace v2rayN.Forms
{
    public partial class MainMsgControl : UserControl
    {
        private string _msgFilter = string.Empty;
        private Regex logFilter;
        private ToolStripLabel logFilterStatus;
        private ToolStripButton autoScrollLog;
        delegate void AppendTextDelegate(string text);

        [DllImport("user32.dll", CharSet = CharSet.Auto)]
        private static extern IntPtr SendMessage(IntPtr window, int message, IntPtr wParam, IntPtr lParam);

        private const int GetFirstVisibleLine = 0x00CE;
        private const int LineScroll = 0x00B6;

        public MainMsgControl()
        {
            InitializeComponent();
            ConfigureLogWorkspace();
        }

        private void MainMsgControl_Load(object sender, EventArgs e)
        {
            SetLogFilter(Utils.RegReadValue(Global.MyRegPath, Utils.MainMsgFilterKey, ""));
        }

        private void ConfigureLogWorkspace()
        {
            var resources = new ComponentResourceManager(typeof(MainMsgControl));
            var uiFont = WorkspaceStyle.MakeFont(this, 9F);
            Font = uiFont;
            BackColor = WorkspaceStyle.Border;
            Padding = new Padding(1);
            gbMsgTitle.BackColor = WorkspaceStyle.Surface;
            txtMsgBox.Dock = DockStyle.Fill;
            txtMsgBox.AccessibleName = resources.GetString("Workspace.Log");
            txtMsgBox.BackColor = WorkspaceStyle.Log;
            txtMsgBox.ForeColor = WorkspaceStyle.LogText;
            txtMsgBox.Font = uiFont;
            ssMain.BackColor = WorkspaceStyle.Surface;
            ssMain.ForeColor = WorkspaceStyle.Muted;
            ssMain.Font = uiFont;
            ssMain.Padding = new Padding(12, 0, 12, 0);
            ssMain.SizingGrip = false;
            toolSslInboundInfo.Margin = new Padding(0, 4, 0, 4);
            toolSslServerSpeed.Available = false;
            toolSslRoutingRule.Available = false;
            toolSslBlank1.Available = false;
            toolSslBlank4.Available = false;

            var toolbar = new ToolStrip
            {
                BackColor = WorkspaceStyle.Surface,
                ForeColor = WorkspaceStyle.Ink,
                Font = uiFont,
                Dock = DockStyle.Top,
                GripStyle = ToolStripGripStyle.Hidden,
                MinimumSize = new Size(0, Math.Max(35, uiFont.Height + 14)),
                Padding = new Padding(12, 0, 9, 0),
                TabStop = true
            };
            toolbar.Renderer = new WorkspaceMenuRenderer(toolbar, dark: false);
            toolbar.Items.Add(new ToolStripLabel(resources.GetString("Workspace.Log"))
            {
                Font = WorkspaceStyle.MakeFont(this, 9F, FontStyle.Bold),
                Margin = new Padding(0, 0, 10, 0)
            });
            logFilterStatus = new ToolStripLabel { ForeColor = WorkspaceStyle.Muted };
            toolbar.Items.Add(logFilterStatus);

            var clear = new ToolStripButton(resources.GetString("Workspace.Clear")) { Alignment = ToolStripItemAlignment.Right };
            clear.Click += menuMsgBoxClear_Click;
            var filter = new ToolStripButton(resources.GetString("Workspace.Filter")) { Alignment = ToolStripItemAlignment.Right };
            filter.Click += menuMsgBoxFilter_Click;
            autoScrollLog = new ToolStripButton(resources.GetString("Workspace.AutoScroll"))
            {
                Alignment = ToolStripItemAlignment.Right,
                CheckOnClick = true,
                Checked = true,
                Name = "autoScrollLog"
            };
            autoScrollLog.CheckedChanged += (sender, args) =>
            {
                if (autoScrollLog.Checked)
                    ScrollToCaret();
            };
            toolbar.Items.AddRange(new ToolStripItem[] { clear, filter, autoScrollLog });
            foreach (var command in new[] { clear, filter, autoScrollLog })
            {
                command.Margin = Padding.Empty;
                command.Padding = new Padding(6, 4, 6, 4);
                command.Overflow = ToolStripItemOverflow.Never;
                command.AutoToolTip = false;
            }
            var logSurface = new Panel
            {
                BackColor = WorkspaceStyle.Log,
                Dock = DockStyle.Fill,
                Padding = new Padding(12, 5, 1, 4)
            };
            logSurface.Controls.Add(txtMsgBox);
            gbMsgTitle.Controls.Add(toolbar);
            gbMsgTitle.Controls.Add(logSurface);
            gbMsgTitle.Controls.SetChildIndex(logSurface, 0);
        }

        private bool SetLogFilter(string pattern)
        {
            try
            {
                logFilter = string.IsNullOrEmpty(pattern)
                    ? null
                    : new Regex(pattern, RegexOptions.None, TimeSpan.FromMilliseconds(100));
            }
            catch (ArgumentException)
            {
                return false;
            }
            _msgFilter = pattern;
            logFilterStatus.Text = pattern;
            return true;
        }

        #region 提示信息

        public void AppendText(string text)
        {
            if (txtMsgBox.InvokeRequired)
            {
                Invoke(new AppendTextDelegate(AppendText), text);
            }
            else
            {
                try
                {
                    if (logFilter != null && !logFilter.IsMatch(text))
                        return;
                }
                catch (RegexMatchTimeoutException)
                {
                    return;
                }
                ShowMsg(text);
            }
        }

        /// <summary>
        /// 提示信息
        /// </summary>
        /// <param name="msg"></param>
        private void ShowMsg(string msg)
        {
            bool follow = autoScrollLog.Checked;
            int firstLine = follow ? 0 : SendMessage(txtMsgBox.Handle, GetFirstVisibleLine, IntPtr.Zero, IntPtr.Zero).ToInt32();
            int selection = txtMsgBox.SelectionStart;
            int selectionLength = txtMsgBox.SelectionLength;
            if (txtMsgBox.Lines.Length > 999)
            {
                ClearMsg();
                firstLine = selection = selectionLength = 0;
            }
            txtMsgBox.AppendText(msg.EndsWith(Environment.NewLine) ? msg : msg + Environment.NewLine);
            if (!follow)
            {
                txtMsgBox.Select(selection, selectionLength);
                int currentLine = SendMessage(txtMsgBox.Handle, GetFirstVisibleLine, IntPtr.Zero, IntPtr.Zero).ToInt32();
                SendMessage(txtMsgBox.Handle, LineScroll, IntPtr.Zero, new IntPtr(firstLine - currentLine));
            }
        }

        /// <summary>
        /// 清除信息
        /// </summary>
        public void ClearMsg()
        {
            txtMsgBox.Invoke((Action)delegate
            {
                txtMsgBox.Clear();
            });
        }

        public void DisplayToolStatus(Config config)
        {
            StringBuilder sb = new StringBuilder();
            sb.Append($"SOCKS5  {config.GetLocalPort(Global.InboundSocks)}");
            sb.Append($"   ·   HTTP  {config.GetLocalPort(Global.InboundHttp)}");

            if (config.inbound[0].allowLANConn)
            {
                sb.Append($"   |   {ResUI.LabLAN}  ");
                sb.Append($"SOCKS5  {config.GetLocalPort(Global.InboundSocks2)}");
                sb.Append($"   ·   HTTP  {config.GetLocalPort(Global.InboundHttp2)}");
            }

            SetToolSslInfo("inbound", sb.ToString());
        }

        public void SetToolSslInfo(string type, string value)
        {
            switch (type)
            {
                case "speed":
                    toolSslServerSpeed.Text = value;
                    toolSslServerSpeed.Available = !string.IsNullOrEmpty(value);
                    break;
                case "inbound":
                    toolSslInboundInfo.Text = value;
                    break;
                case "routing":
                    toolSslRoutingRule.Text = value;
                    break;
            }

        }

        public void ScrollToCaret()
        {
            if (autoScrollLog.Checked)
            {
                txtMsgBox.Select(txtMsgBox.TextLength, 0);
                txtMsgBox.ScrollToCaret();
            }
        }
        #endregion


        #region MsgBoxMenu
        private void menuMsgBoxSelectAll_Click(object sender, EventArgs e)
        {
            txtMsgBox.Focus();
            txtMsgBox.SelectAll();
        }

        private void menuMsgBoxCopy_Click(object sender, EventArgs e)
        {
            var data = txtMsgBox.SelectedText.TrimEx();
            Utils.SetClipboardData(data);
        }

        private void menuMsgBoxCopyAll_Click(object sender, EventArgs e)
        {
            var data = txtMsgBox.Text;
            Utils.SetClipboardData(data);
        }
        private void menuMsgBoxClear_Click(object sender, EventArgs e)
        {
            txtMsgBox.Clear();
        }
        private void menuMsgBoxAddRoutingRule_Click(object sender, EventArgs e)
        {
            menuMsgBoxCopy_Click(null, null);
            var fm = new RoutingSettingForm();
            fm.ShowDialog();

        }

        private void txtMsgBox_KeyDown(object sender, KeyEventArgs e)
        {
            if (e.Control)
            {
                switch (e.KeyCode)
                {
                    case Keys.A:
                        menuMsgBoxSelectAll_Click(null, null);
                        break;
                    case Keys.C:
                        menuMsgBoxCopy_Click(null, null);
                        break;
                    case Keys.V:
                        menuMsgBoxAddRoutingRule_Click(null, null);
                        break;

                }
            }

        }
        private void menuMsgBoxFilter_Click(object sender, EventArgs e)
        {
            var fm = new MsgFilterSetForm();
            fm.MsgFilter = _msgFilter;
            fm.ShowDefFilter = true;
            if (fm.ShowDialog(FindForm()) == DialogResult.OK)
            {
                if (!SetLogFilter(fm.MsgFilter))
                {
                    UI.ShowWarning(new ComponentResourceManager(typeof(MainMsgControl)).GetString("Workspace.InvalidFilter"));
                    return;
                }
                Utils.RegWriteValue(Global.MyRegPath, Utils.MainMsgFilterKey, _msgFilter);
            }
        }

        private void ssMain_ItemClicked(object sender, ToolStripItemClickedEventArgs e)
        {
            if (!Utils.IsNullOrEmpty(e.ClickedItem.Text))
            {
                Utils.SetClipboardData(e.ClickedItem.Text);
            }
        }
        #endregion


    }
}
