using System;
using System.Drawing;
using System.Globalization;
using System.Windows.Forms;

namespace v2rayN.Forms
{
    internal static class WorkspaceStyle
    {
        internal static Color Canvas => SystemInformation.HighContrast ? SystemColors.Control : Color.FromArgb(239, 243, 248);
        internal static Color Surface => SystemInformation.HighContrast ? SystemColors.Window : Color.White;
        internal static Color Border => SystemInformation.HighContrast ? SystemColors.WindowText : Color.FromArgb(209, 220, 234);
        internal static Color Ink => SystemInformation.HighContrast ? SystemColors.WindowText : Color.FromArgb(23, 40, 65);
        internal static Color Muted => SystemInformation.HighContrast ? SystemColors.WindowText : Color.FromArgb(74, 93, 120);
        internal static Color Accent => SystemInformation.HighContrast ? SystemColors.Highlight : Color.FromArgb(34, 83, 165);
        internal static Color AccentText => SystemInformation.HighContrast ? SystemColors.HighlightText : Color.White;
        internal static Color Selected => SystemInformation.HighContrast ? SystemColors.Highlight : Color.FromArgb(228, 239, 255);
        internal static Color SelectedText => SystemInformation.HighContrast ? SystemColors.HighlightText : Accent;
        internal static Color Header => SystemInformation.HighContrast ? SystemColors.Menu : Color.FromArgb(24, 42, 69);
        internal static Color HeaderText => SystemInformation.HighContrast ? SystemColors.MenuText : Color.FromArgb(237, 244, 255);
        internal static Color HeaderHover => SystemInformation.HighContrast ? SystemColors.Highlight : Color.FromArgb(51, 76, 111);
        internal static Color Running => SystemInformation.HighContrast ? SystemColors.WindowText : Color.FromArgb(18, 105, 80);
        internal static Color Log => SystemInformation.HighContrast ? SystemColors.Window : Color.FromArgb(20, 34, 54);
        internal static Color LogText => SystemInformation.HighContrast ? SystemColors.WindowText : Color.FromArgb(224, 235, 251);

        internal static Font MakeFont(Control owner, float size, FontStyle style = FontStyle.Regular)
        {
            string family = CultureInfo.CurrentUICulture.TwoLetterISOLanguageName == "zh" ? "Microsoft YaHei" : "Segoe UI";
            var font = new Font(family, size, style, GraphicsUnit.Point);
            owner.Disposed += (sender, args) => font.Dispose();
            return font;
        }

        internal static Button Command(string text, bool primary = false)
        {
            var button = new Button
            {
                AutoSize = true,
                AutoSizeMode = AutoSizeMode.GrowAndShrink,
                BackColor = primary ? Accent : Surface,
                FlatStyle = FlatStyle.Flat,
                ForeColor = primary ? AccentText : Ink,
                Padding = new Padding(10, 0, 10, 0),
                Text = text,
                UseVisualStyleBackColor = false
            };
            button.FlatAppearance.BorderColor = primary ? Accent : Border;
            button.FlatAppearance.MouseOverBackColor = primary ? HeaderHover : Canvas;
            button.FlatAppearance.MouseDownBackColor = primary ? Header : Selected;
            return button;
        }
    }

    internal sealed class WorkspaceMenuRenderer : ToolStripProfessionalRenderer
    {
        private readonly ToolStrip commandBar;
        private readonly bool dark;

        internal WorkspaceMenuRenderer(ToolStrip commandBar, bool dark = true)
        {
            this.commandBar = commandBar;
            this.dark = dark;
            RoundedEdges = false;
        }

        protected override void OnRenderToolStripBackground(ToolStripRenderEventArgs e)
        {
            if (e.ToolStrip != commandBar)
            {
                base.OnRenderToolStripBackground(e);
                return;
            }
            using (var brush = new SolidBrush(dark ? WorkspaceStyle.Header : WorkspaceStyle.Surface))
                e.Graphics.FillRectangle(brush, e.AffectedBounds);
        }

        protected override void OnRenderToolStripBorder(ToolStripRenderEventArgs e)
        {
            if (e.ToolStrip != commandBar)
                base.OnRenderToolStripBorder(e);
        }

        protected override void OnRenderDropDownButtonBackground(ToolStripItemRenderEventArgs e)
        {
            if (e.ToolStrip == commandBar)
                PaintCommandBackground(e);
            else
                base.OnRenderDropDownButtonBackground(e);
        }

        protected override void OnRenderButtonBackground(ToolStripItemRenderEventArgs e)
        {
            if (e.ToolStrip == commandBar)
                PaintCommandBackground(e);
            else
                base.OnRenderButtonBackground(e);
        }

        protected override void OnRenderOverflowButtonBackground(ToolStripItemRenderEventArgs e)
        {
            if (e.ToolStrip == commandBar)
            {
                PaintCommandBackground(e);
                TextRenderer.DrawText(e.Graphics, "»", e.Item.Font, new Rectangle(Point.Empty, e.Item.Size),
                    dark ? WorkspaceStyle.HeaderText : WorkspaceStyle.Muted,
                    TextFormatFlags.HorizontalCenter | TextFormatFlags.VerticalCenter | TextFormatFlags.NoPadding);
            }
            else
                base.OnRenderOverflowButtonBackground(e);
        }

        protected override void OnRenderArrow(ToolStripArrowRenderEventArgs e)
        {
            if (e.Item?.GetCurrentParent() == commandBar)
                e.ArrowColor = dark ? WorkspaceStyle.HeaderText : WorkspaceStyle.Muted;
            base.OnRenderArrow(e);
        }

        protected override void OnRenderItemText(ToolStripItemTextRenderEventArgs e)
        {
            if (e.ToolStrip == commandBar)
                e.TextColor = dark ? WorkspaceStyle.HeaderText : e.Item.ForeColor;
            base.OnRenderItemText(e);
        }

        private void PaintCommandBackground(ToolStripItemRenderEventArgs e)
        {
            if (e.Item.Selected || e.Item.Pressed || (e.Item is ToolStripButton button && button.Checked))
            {
                Rectangle bounds = new Rectangle(Point.Empty, e.Item.Size);
                bounds.Inflate(-2, -3);
                using (var brush = new SolidBrush(dark ? WorkspaceStyle.HeaderHover : WorkspaceStyle.Selected))
                    e.Graphics.FillRectangle(brush, bounds);
            }
        }
    }
}
