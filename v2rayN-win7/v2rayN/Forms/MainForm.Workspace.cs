using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Drawing;
using System.Linq;
using System.Runtime.InteropServices;
using System.Windows.Forms;
using v2rayN.Handler;
using v2rayN.Mode;
using v2rayN.Resx;

namespace v2rayN.Forms
{
    public partial class MainForm
    {
        private static readonly ComponentResourceManager WorkspaceResources = new ComponentResourceManager(typeof(MainForm));

        private TextBox profileSearch;
        private ComboBox groupPicker;
        private ComboBox subscriptionPicker;
        private ComboBox proxyPicker;
        private ComboBox routePicker;
        private Label routeCaption;
        private Label profileCount;
        private Label profileEmpty;
        private Label coreState;
        private Label activeProfile;
        private Button activateProfile;
        private Button fastRealPing;
        private ToolTip workspaceHints;
        private int fieldHeight;
        private bool updatingPickers;
        private bool layoutSplitAdjusted;

        [DllImport("user32.dll", CharSet = CharSet.Unicode)]
        private static extern IntPtr SendMessage(IntPtr window, int message, IntPtr wParam, string lParam);

        private sealed class WorkspaceChoice
        {
            public WorkspaceChoice(string text, string id)
            {
                Text = text;
                Id = id;
            }

            public string Text { get; }
            public string Id { get; }
            public override string ToString() => Text;
        }

        private static string WorkspaceText(string name) => WorkspaceResources.GetString(name);

        private static bool IsWindows7 => Environment.OSVersion.Version.Major == 6 &&
            Environment.OSVersion.Version.Minor == 1;

        private void InitializeWorkspace()
        {
            SuspendLayout();
            Font = WorkspaceStyle.MakeFont(this, 9F);
            fieldHeight = Math.Max(34, Font.Height + 15);
            workspaceHints = new ToolTip(components);
            ClientSize = new Size(1120, 690);
            MinimumSize = new Size(720, 510);
            StartPosition = FormStartPosition.CenterScreen;
            BackColor = WorkspaceStyle.Canvas;
            ForeColor = WorkspaceStyle.Ink;

            ConfigureCommandBar();
            ConfigureConnectionBar();
            ConfigureProfiles();

            scBig.Margin = Padding.Empty;
            scBig.Panel1.Padding = new Padding(18, 0, 18, 0);
            scBig.Panel2.Padding = new Padding(18, 0, 18, 14);
            scBig.SplitterWidth = 10;
            scBig.Panel1MinSize = 156;
            scBig.Panel2MinSize = 112;
            scBig.FixedPanel = FixedPanel.Panel2;
            scBig.SplitterMoving += (sender, args) => layoutSplitAdjusted = true;
            scBig.SizeChanged += (sender, args) => FitWorkspaceSplit();
            mainMsgControl.Dock = DockStyle.Fill;
            ResumeLayout(true);
            FitWorkspaceSplit();
        }

        private void FitWorkspaceSplit()
        {
            int minimumLog = scBig.Panel2MinSize + scBig.SplitterWidth;
            if (layoutSplitAdjusted || scBig.Height < scBig.Panel1MinSize + minimumLog)
                return;

            int logHeight = Math.Min(160, Math.Max(minimumLog, scBig.Height - 245));
            scBig.SplitterDistance = Math.Max(scBig.Panel1MinSize, scBig.Height - logHeight);
        }

        private void ConfigureCommandBar()
        {
            tsMain.SuspendLayout();
            tsMain.GripStyle = ToolStripGripStyle.Hidden;
            tsMain.BackColor = WorkspaceStyle.Header;
            tsMain.ForeColor = WorkspaceStyle.HeaderText;
            tsMain.Font = Font;
            tsMain.Padding = new Padding(16, 3, 16, 3);
            tsMain.MinimumSize = new Size(0, 46);
            tsMain.Renderer = new WorkspaceMenuRenderer(tsMain);

            foreach (ToolStripItem item in tsMain.Items)
            {
                if (item is ToolStripSeparator)
                {
                    item.Available = false;
                    continue;
                }
                item.DisplayStyle = ToolStripItemDisplayStyle.Text;
                item.TextAlign = ContentAlignment.MiddleCenter;
                item.Padding = new Padding(8, 5, 8, 5);
                item.Margin = new Padding(2, 1, 2, 1);
            }

            var brand = new ToolStripLabel("v2rayN")
            {
                Font = WorkspaceStyle.MakeFont(this, 15F, FontStyle.Bold),
                Margin = new Padding(2, 0, 15, 0),
                Overflow = ToolStripItemOverflow.Never
            };
            tsMain.Items.Insert(0, brand);

            var promotion = new ToolStripMenuItem(tsbPromotion.Text);
            promotion.Click += tsbPromotion_Click;
            tsbHelp.DropDownItems.Add(promotion);
            tsbPromotion.Available = false;
            tsbClose.Available = false;
            if (IsWindows7)
                tsbCheckUpdateXrayCore.Text = WorkspaceText("Workspace.XrayWin7Releases");
            tsMain.ResumeLayout();
        }

        private void ConfigureConnectionBar()
        {
            panel1.Height = fieldHeight + Font.Height + 41;
            panel1.Padding = new Padding(18, 12, 18, 10);
            var frame = new Panel { BackColor = WorkspaceStyle.Border, Dock = DockStyle.Fill, Padding = new Padding(1) };
            var connection = new TableLayoutPanel
            {
                BackColor = WorkspaceStyle.Surface,
                ColumnCount = 3,
                Dock = DockStyle.Fill,
                Padding = new Padding(12, 5, 12, 5),
                RowCount = 2
            };
            connection.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 40));
            connection.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 30));
            connection.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 30));
            connection.RowStyles.Add(new RowStyle(SizeType.Absolute, Font.Height + 5));
            connection.RowStyles.Add(new RowStyle(SizeType.Percent, 100));

            coreState = ConnectionCaption(string.Empty);
            activeProfile = ConnectionCaption(WorkspaceText("Workspace.NoActiveProfile"));
            activeProfile.Font = WorkspaceStyle.MakeFont(this, 12F, FontStyle.Bold);
            activeProfile.ForeColor = WorkspaceStyle.Ink;
            activeProfile.TextAlign = ContentAlignment.MiddleLeft;

            proxyPicker = ConnectionPicker("proxyPicker", ResUI.SystemProxy);
            proxyPicker.Items.AddRange(new object[]
            {
                WorkspaceText("Workspace.ClearProxy"),
                WorkspaceText("Workspace.SetProxy"),
                WorkspaceText("Workspace.KeepProxy")
            });
            proxyPicker.SelectionChangeCommitted += (sender, args) =>
            {
                if (proxyPicker.SelectedIndex >= 0)
                    SetListenerType((ESysProxyType)proxyPicker.SelectedIndex);
            };

            routeCaption = ConnectionCaption(menuRoutings.Text);
            routePicker = ConnectionPicker("routePicker", menuRoutings.Text);
            routePicker.SelectionChangeCommitted += (sender, args) =>
            {
                if (routePicker.SelectedItem is ToolStripMenuItem route)
                    ts_Routing_Click(route, EventArgs.Empty);
            };

            connection.Controls.Add(coreState, 0, 0);
            connection.Controls.Add(activeProfile, 0, 1);
            connection.Controls.Add(ConnectionCaption(ResUI.SystemProxy), 1, 0);
            connection.Controls.Add(FramePicker(proxyPicker), 1, 1);
            connection.Controls.Add(routeCaption, 2, 0);
            connection.Controls.Add(FramePicker(routePicker), 2, 1);
            frame.Controls.Add(connection);
            panel1.Controls.Add(frame);
        }

        private static Label ConnectionCaption(string text) => new Label
        {
            AutoEllipsis = true,
            Dock = DockStyle.Fill,
            ForeColor = WorkspaceStyle.Muted,
            Margin = new Padding(0, 0, 12, 0),
            Text = text,
            TextAlign = ContentAlignment.MiddleLeft
        };

        private ComboBox ConnectionPicker(string name, string label)
        {
            var picker = new ComboBox
            {
                AccessibleName = label,
                Anchor = AnchorStyles.Left | AnchorStyles.Right,
                BackColor = WorkspaceStyle.Surface,
                DisplayMember = "Text",
                DrawMode = DrawMode.OwnerDrawFixed,
                DropDownStyle = ComboBoxStyle.DropDownList,
                DropDownHeight = 300,
                FlatStyle = FlatStyle.Flat,
                ForeColor = WorkspaceStyle.Ink,
                IntegralHeight = false,
                ItemHeight = fieldHeight - 9,
                Margin = new Padding(0, 0, 12, 0),
                Name = name
            };
            picker.DrawItem += (sender, e) =>
            {
                e.DrawBackground();
                if (e.Index >= 0)
                {
                    Rectangle bounds = e.Bounds;
                    bounds.Inflate(-8, 0);
                    TextRenderer.DrawText(e.Graphics, picker.GetItemText(picker.Items[e.Index]), picker.Font,
                        bounds, e.ForeColor, TextFormatFlags.SingleLine | TextFormatFlags.VerticalCenter |
                        TextFormatFlags.EndEllipsis | TextFormatFlags.NoPadding);
                }
                e.DrawFocusRectangle();
            };
            workspaceHints.SetToolTip(picker, label);
            return picker;
        }

        private Panel FramePicker(ComboBox picker)
        {
            var frame = new TableLayoutPanel
            {
                Anchor = AnchorStyles.Left | AnchorStyles.Right,
                ColumnCount = 1,
                Height = fieldHeight - 1,
                Margin = picker.Margin,
                Padding = new Padding(1),
                RowCount = 1
            };
            frame.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100));
            frame.RowStyles.Add(new RowStyle(SizeType.Percent, 100));
            frame.Paint += (sender, args) => ControlPaint.DrawBorder(args.Graphics, frame.ClientRectangle,
                picker.Focused ? WorkspaceStyle.Accent : WorkspaceStyle.Border, ButtonBorderStyle.Solid);
            picker.Margin = Padding.Empty;
            picker.GotFocus += (sender, args) => frame.Invalidate();
            picker.LostFocus += (sender, args) => frame.Invalidate();
            frame.Controls.Add(picker, 0, 0);
            return frame;
        }

        private void ConfigureProfiles()
        {
            var border = new Panel { BackColor = WorkspaceStyle.Border, Dock = DockStyle.Fill, Padding = new Padding(1) };
            var card = new Panel { BackColor = WorkspaceStyle.Surface, Dock = DockStyle.Fill };
            border.Controls.Add(card);
            scBig.Panel1.Controls.Add(border);

            scServers.BackColor = WorkspaceStyle.Surface;
            scServers.Dock = DockStyle.Fill;
            scServers.Panel2Collapsed = true;
            lvServers.BackColor = WorkspaceStyle.Surface;
            lvServers.ForeColor = WorkspaceStyle.Ink;
            lvServers.BorderStyle = BorderStyle.None;
            lvServers.AccessibleName = WorkspaceText("Workspace.Profiles");
            lvServers.ShowItemToolTips = true;
            lvServers.SmallImageList = new ImageList(components) { ImageSize = new Size(1, Font.Height + 14) };
            lvServers.OwnerDraw = !SystemInformation.HighContrast;
            lvServers.DrawColumnHeader += DrawProfileColumnHeader;
            lvServers.DrawItem += (sender, e) => e.DrawDefault = true;
            lvServers.DrawSubItem += (sender, e) => e.DrawDefault = true;

            profileEmpty = new Label
            {
                AutoEllipsis = true,
                BackColor = WorkspaceStyle.Surface,
                Dock = DockStyle.Fill,
                ForeColor = WorkspaceStyle.Muted,
                Padding = new Padding(18, 0, 18, 0),
                TextAlign = ContentAlignment.MiddleCenter
            };
            scServers.Panel1.Controls.Add(profileEmpty);

            var heading = new TableLayoutPanel
            {
                ColumnCount = 2,
                Dock = DockStyle.Top,
                Height = fieldHeight + 15,
                Padding = new Padding(12, 6, 12, 4),
                RowCount = 1
            };
            heading.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100));
            heading.ColumnStyles.Add(new ColumnStyle(SizeType.AutoSize));
            heading.RowStyles.Add(new RowStyle(SizeType.Percent, 100));
            profileCount = ConnectionCaption(string.Empty);
            profileCount.Font = WorkspaceStyle.MakeFont(this, 12F, FontStyle.Bold);
            profileCount.ForeColor = WorkspaceStyle.Ink;
            heading.Controls.Add(profileCount, 0, 0);
            heading.Controls.Add(CreateProfileActions(), 1, 0);

            var filters = new TableLayoutPanel
            {
                ColumnCount = 3,
                Dock = DockStyle.Top,
                Height = fieldHeight + 10,
                Padding = new Padding(12, 0, 12, 8),
                RowCount = 1
            };
            filters.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100));
            filters.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 48));
            filters.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 48));
            filters.RowStyles.Add(new RowStyle(SizeType.Percent, 100));
            filters.Controls.Add(CreateSearchField(), 0, 0);

            groupPicker = ConnectionPicker("groupPicker", WorkspaceText("Workspace.Groups"));
            groupPicker.SelectedIndexChanged += (sender, args) =>
            {
                if (updatingPickers)
                    return;

                _groupId = (groupPicker.SelectedItem as WorkspaceChoice)?.Id ?? string.Empty;
                RefreshServers();
            };
            filters.Controls.Add(FramePicker(groupPicker), 1, 0);

            subscriptionPicker = ConnectionPicker("subscriptionPicker", tsbSub.Text);
            subscriptionPicker.Margin = Padding.Empty;
            subscriptionPicker.SelectedIndexChanged += (sender, args) =>
            {
                if (!updatingPickers)
                    RefreshServers();
            };
            filters.Controls.Add(FramePicker(subscriptionPicker), 2, 0);

            card.Controls.Add(scServers);
            card.Controls.Add(filters);
            card.Controls.Add(heading);
        }

        private Panel CreateSearchField()
        {
            var field = new TableLayoutPanel
            {
                BackColor = WorkspaceStyle.Surface,
                ColumnCount = 3,
                Height = fieldHeight - 1,
                Anchor = AnchorStyles.Left | AnchorStyles.Right,
                Margin = new Padding(0, 0, 12, 0),
                Padding = new Padding(1),
                RowCount = 1
            };
            field.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 31));
            field.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100));
            field.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 32));
            field.RowStyles.Add(new RowStyle(SizeType.Percent, 100));

            var searchIcon = new Panel { Dock = DockStyle.Fill, Margin = Padding.Empty };
            searchIcon.Paint += (sender, args) =>
            {
                int center = searchIcon.Height / 2;
                args.Graphics.SmoothingMode = System.Drawing.Drawing2D.SmoothingMode.AntiAlias;
                using (var pen = new Pen(WorkspaceStyle.Muted, 1.4f))
                {
                    args.Graphics.DrawEllipse(pen, 9, center - 7, 10, 10);
                    args.Graphics.DrawLine(pen, 17, center + 2, 22, center + 7);
                }
            };
            searchIcon.Click += (sender, args) => profileSearch.Focus();
            field.Controls.Add(searchIcon, 0, 0);

            profileSearch = new TextBox
            {
                AccessibleName = WorkspaceText("Workspace.Search"),
                Anchor = AnchorStyles.Left | AnchorStyles.Right,
                BackColor = WorkspaceStyle.Surface,
                BorderStyle = BorderStyle.None,
                ForeColor = WorkspaceStyle.Ink,
                Margin = Padding.Empty,
                Name = "profileSearch"
            };
            field.Paint += (sender, args) => ControlPaint.DrawBorder(args.Graphics, field.ClientRectangle,
                profileSearch.Focused ? WorkspaceStyle.Accent : WorkspaceStyle.Border, ButtonBorderStyle.Solid);
            profileSearch.GotFocus += (sender, args) => field.Invalidate();
            profileSearch.LostFocus += (sender, args) => field.Invalidate();
            profileSearch.HandleCreated += (sender, args) =>
                SendMessage(profileSearch.Handle, 0x1501, IntPtr.Zero, profileSearch.AccessibleName); // EM_SETCUEBANNER
            profileSearch.TextChanged += (sender, args) =>
            {
                serverFilter = profileSearch.Text.Trim();
                RefreshServers();
            };
            field.Controls.Add(profileSearch, 1, 0);

            var clear = new Button
            {
                AccessibleName = WorkspaceText("Workspace.ClearSearch"),
                BackColor = WorkspaceStyle.Surface,
                Dock = DockStyle.Fill,
                FlatStyle = FlatStyle.Flat,
                ForeColor = WorkspaceStyle.Muted,
                Margin = Padding.Empty,
                Text = "×",
                UseVisualStyleBackColor = false,
                Visible = false
            };
            clear.FlatAppearance.BorderSize = 0;
            clear.Click += (sender, args) => { profileSearch.Clear(); profileSearch.Focus(); };
            profileSearch.TextChanged += (sender, args) => clear.Visible = profileSearch.TextLength > 0;
            workspaceHints.SetToolTip(clear, clear.AccessibleName);
            field.Controls.Add(clear, 2, 0);
            return field;
        }

        private FlowLayoutPanel CreateProfileActions()
        {
            var actions = new FlowLayoutPanel
            {
                Anchor = AnchorStyles.Right,
                AutoSize = true,
                AutoSizeMode = AutoSizeMode.GrowAndShrink,
                Margin = Padding.Empty,
                WrapContents = false
            };
            var import = ProfileCommand(WorkspaceText("Workspace.Import"));
            workspaceHints.SetToolTip(import, menuAddServers.Text);
            import.Click += menuAddServers_Click;
            activateProfile = ProfileCommand(WorkspaceText("Workspace.Activate"));
            activateProfile.Enabled = false;
            activateProfile.Click += menuSetDefaultServer_Click;

            var more = ProfileCommand(WorkspaceText("Workspace.More") + "  ▾");
            var menu = new ContextMenuStrip(components);
            foreach (var item in new[] { menuTcpingServer, menuRealPingServer, menuSpeedServer, menuPingServer, menuSortServerResult })
            {
                var action = new ToolStripMenuItem(item.Text);
                action.Click += (sender, args) => item.PerformClick();
                menu.Items.Add(action);
            }
            var fit = new ToolStripMenuItem(WorkspaceText("Workspace.FitColumns"));
            fit.Click += (sender, args) => FitProfileColumns();
            menu.Items.Add(new ToolStripSeparator());
            menu.Items.Add(fit);
            more.Click += (sender, args) => menu.Show(more, new Point(0, more.Height));

            fastRealPing = ProfileCommand(WorkspaceText("Workspace.FastRealPing"), primary: true);
            fastRealPing.Name = "fastRealPing";
            fastRealPing.Enabled = false;
            fastRealPing.Margin = Padding.Empty;
            fastRealPing.Click += FastRealPing_Click;
            RefreshLatencyCommand(false);

            actions.Controls.AddRange(new Control[] { import, activateProfile, more, fastRealPing });
            return actions;
        }

        private Button ProfileCommand(string text, bool primary = false)
        {
            var button = WorkspaceStyle.Command(text, primary);
            button.MinimumSize = new Size(0, fieldHeight);
            button.Margin = new Padding(0, 0, 6, 0);
            return button;
        }

        private void RefreshLatencyCommand(bool running)
        {
            fastRealPing.Text = WorkspaceText(running ? "Workspace.CancelLatency" : "Workspace.FastRealPing");
            fastRealPing.AccessibleName = fastRealPing.Text;
            fastRealPing.Enabled = running || lstVmess?.Exists(node => node.configType != EConfigType.Custom) == true;
            workspaceHints.SetToolTip(fastRealPing, WorkspaceText(running ? "Workspace.CancelLatency" : "Workspace.FastRealPingTip"));
        }

        private static void DrawProfileColumnHeader(object sender, DrawListViewColumnHeaderEventArgs e)
        {
            using (var brush = new SolidBrush(WorkspaceStyle.Canvas))
                e.Graphics.FillRectangle(brush, e.Bounds);
            using (var pen = new Pen(WorkspaceStyle.Border))
                e.Graphics.DrawLine(pen, e.Bounds.Left, e.Bounds.Bottom - 1, e.Bounds.Right, e.Bounds.Bottom - 1);

            Rectangle textBounds = e.Bounds;
            textBounds.Inflate(-6, 0);
            var alignment = e.Header.TextAlign == HorizontalAlignment.Right ? TextFormatFlags.Right : TextFormatFlags.Left;
            TextRenderer.DrawText(e.Graphics, e.Header.Text, e.Font, textBounds, WorkspaceStyle.Muted,
                alignment | TextFormatFlags.VerticalCenter | TextFormatFlags.SingleLine | TextFormatFlags.EndEllipsis |
                TextFormatFlags.NoPadding);
        }

        private void SyncSubscriptions()
        {
            var current = (subscriptionPicker.SelectedItem as WorkspaceChoice)?.Id ?? string.Empty;
            var choices = config.subItem.Select(sub => new WorkspaceChoice(sub.remarks, sub.id)).ToList();
            choices.Insert(0, new WorkspaceChoice(WorkspaceText("Workspace.AllSubscriptions"), string.Empty));
            UpdatePicker(subscriptionPicker, choices, current);
        }

        private void UpdatePicker(ComboBox picker, List<WorkspaceChoice> choices, string current)
        {
            if (choices.Count == picker.Items.Count &&
                Enumerable.Range(0, choices.Count).All(index =>
                    picker.Items[index] is WorkspaceChoice choice &&
                    choice.Id == choices[index].Id && choice.Text == choices[index].Text))
                return;

            updatingPickers = true;
            picker.BeginUpdate();
            try
            {
                picker.Items.Clear();
                picker.Items.AddRange(choices.ToArray());
                picker.SelectedIndex = Math.Max(0, choices.FindIndex(choice => choice.Id == current));
            }
            finally
            {
                picker.EndUpdate();
                updatingPickers = false;
            }
        }

        private void SyncConnectionBar()
        {
            var selected = config.vmess.Find(config.IsActiveNode);
            var core = selected == null ? null : LazyConfig.Instance.GetCoreType(selected, selected.configType).ToString();
            bool running = v2rayHandler?.IsRunning == true;
            var state = WorkspaceText(running ? "Workspace.CoreRunning" : "Workspace.CoreStopped");
            coreState.Text = core == null ? state : $"{state}  ·  {core}";
            coreState.ForeColor = running ? WorkspaceStyle.Running : WorkspaceStyle.Muted;
            activeProfile.Text = selected?.remarks ?? WorkspaceText("Workspace.NoActiveProfile");
            int proxyMode = (int)config.sysProxyType;
            proxyPicker.SelectedIndex = proxyMode >= 0 && proxyMode < proxyPicker.Items.Count ? proxyMode : -1;
        }

        private void SyncRoutePicker()
        {
            routeCaption.Visible = routePicker.Parent.Visible = config.enableRoutingAdvanced;
            routePicker.BeginUpdate();
            try
            {
                routePicker.Items.Clear();
                foreach (ToolStripMenuItem route in menuRoutings.DropDownItems)
                {
                    routePicker.Items.Add(route);
                    if (route.Checked)
                        routePicker.SelectedItem = route;
                }
            }
            finally
            {
                routePicker.EndUpdate();
            }
        }

        private void SyncProfileCount()
        {
            profileCount.Text = string.Format(WorkspaceText("Workspace.ProfileCount"), lvServers.Items.Count);
            profileEmpty.Text = config.vmess.Count == 0
                ? WorkspaceText("Workspace.EmptyList")
                : WorkspaceText("Workspace.NoMatches");
            bool empty = lvServers.Items.Count == 0;
            profileEmpty.Visible = empty;
            lvServers.Visible = !empty;
            fastRealPing.Enabled = latencyCancellation != null || lstVmess.Exists(node => node.configType != EConfigType.Custom);
            lvServers_SelectedIndexChanged(lvServers, EventArgs.Empty);
        }

        private static void ApplyProfileAppearance(ListViewItem row, int index, bool isActive)
        {
            row.Text = isActive ? Global.CheckMark : (index + 1).ToString();
            row.ForeColor = isActive ? WorkspaceStyle.SelectedText : WorkspaceStyle.Ink;
            row.BackColor = isActive ? WorkspaceStyle.Selected : WorkspaceStyle.Surface;
        }

        private static void ShowProfileTestResult(ListViewItem row, string result)
        {
            bool failed = result == ResUI.OperationFailed;
            row.SubItems[EServerColName.testResult.ToString()].Text = failed
                ? WorkspaceText("Workspace.TestFailed")
                : result;
            row.ToolTipText = failed ? result : string.Empty;
        }

        private void FitProfileColumns()
        {
            foreach (ColumnHeader column in lvServers.Columns)
            {
                column.Width = -1;
                int contentWidth = column.Width;
                column.Width = -2;
                column.Width = Math.Max(column.Width, contentWidth + 8);
            }
        }

        protected override bool ProcessCmdKey(ref Message msg, Keys keyData)
        {
            if (keyData == (Keys.Control | Keys.F) && profileSearch != null)
            {
                profileSearch.Focus();
                profileSearch.SelectAll();
                return true;
            }
            if (keyData == (Keys.Control | Keys.V) && config != null &&
                !profileSearch.Focused && !mainMsgControl.ContainsFocus)
            {
                menuAddServers_Click(this, EventArgs.Empty);
                return true;
            }
            if (keyData == Keys.Escape && profileSearch?.Focused == true)
            {
                profileSearch.Clear();
                return true;
            }
            return base.ProcessCmdKey(ref msg, keyData);
        }
    }
}
