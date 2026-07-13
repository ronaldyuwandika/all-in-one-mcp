#!/usr/bin/env python3
"""vault watch \u2014 TUI monitor for credential vault.

Keys:
  q        quit
  r        manual refresh
  /        activate search filter
  Escape   clear search / close detail
  t        toggle dark/light theme
  d        show selected credential detail
  Tab      cycle focus panels
"""

import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import server as sv

from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, Vertical, Container
from textual.screen import ModalScreen
from textual.widgets import Header, Footer, DataTable, Static, RichLog, Input, Label, Button
from textual import work


DARK_CSS = """
Screen { background: #1a1b26; }
#main-container { height: 100%; }
#left-panel { width: 2fr; height: 100%; border: solid #3b4261; }
#right-panel { width: 1fr; height: 100%; border: solid #3b4261; }
#top-bar { height: 1; background: #24283b; color: #565f89; padding: 0 1; }
#creds-table { height: 1fr; }
#file-status { height: auto; max-height: 6; border-top: solid #3b4261; padding: 0 1; background: #1f2335; }
#audit-log { height: 1fr; }
#search-bar { dock: bottom; height: 1; padding: 0 1; background: #24283b; visibility: hidden; }
#search-bar.focused { visibility: visible; }
#status-bar { dock: bottom; height: 1; background: #24283b; color: #565f89; padding: 0 1; }
DataTable { height: 1fr; }
Static { color: #a9b1d6; }
RichLog { background: #1f2335; }
Input { background: #1a1b26; color: #a9b1d6; }
"""


class CredentialDetail(ModalScreen):
    def __init__(self, name, cred_type, length, source, purpose="unknown"):
        super().__init__()
        self.cred_name = name
        self.cred_type = cred_type
        self.cred_length = length
        self.cred_source = source
        self.cred_purpose = purpose

    CSS = """
    #detail-box { width: 40; height: auto; border: solid $accent; background: $surface; padding: 1 2; margin: 4 8; }
    Button { margin: 1 0 0 0; }
    """

    def compose(self):
        with Container(id="detail-box"):
            yield Label(f"[bold]{self.cred_name}[/]")
            yield Static(f"Type:    {self.cred_type}")
            yield Static(f"Length:  {self.cred_length} chars")
            yield Static(f"Source:  {self.cred_source}")
            yield Static(f"Purpose: {self.cred_purpose}")
            yield Button("Close", variant="primary", id="close-detail")

    def on_button_pressed(self, event):
        self.app.pop_screen()


class VaultMonitor(App):
    MODES = {"dark": DARK_CSS}
    CURRENT_MODE = "dark"

    BINDINGS = [
        Binding("q", "quit", "Quit"),
        Binding("r", "refresh", "Refresh"),
        Binding("slash", "search", "Search"),
        Binding("escape", "clear_search", "Clear"),
        Binding("t", "toggle_theme", "Theme"),
        Binding("d", "show_detail", "Detail"),
        Binding("tab", "cycle_focus", "Focus"),
    ]

    poll_interval: float = 2.0
    show_audit: bool = True
    _filter: str = ""

    def __init__(self, interval=2.0, show_audit=True):
        super().__init__()
        self.poll_interval = interval
        self.show_audit = show_audit
        self._poll_count = 0

    def compose(self) -> ComposeResult:
        yield Header()
        with Container(id="main-container"):
            with Horizontal():
                with Vertical(id="left-panel"):
                    yield DataTable(id="creds-table", cursor_type="row")
                    yield Static(id="file-status")
                with Vertical(id="right-panel"):
                    yield RichLog(id="audit-log", highlight=True, wrap=True)
        yield Input(id="search-bar", placeholder="Filter credentials...")
        yield Static(id="status-bar")
        yield Footer()

    def on_mount(self) -> None:
        table = self.query_one("#creds-table", DataTable)
        table.add_columns("Name", "Type", "Length", "Source")
        search = self.query_one("#search-bar", Input)
        search.visible = False
        self.set_interval(self.poll_interval, self._refresh)
        self._refresh()
        self._update_status_bar()

    def action_refresh(self):
        self._refresh()

    def action_search(self):
        search = self.query_one("#search-bar", Input)
        search.visible = True
        search.focus()

    def action_clear_search(self):
        search = self.query_one("#search-bar", Input)
        if search.visible:
            search.value = ""
            search.visible = False
            self._filter = ""
            self.query_one(DataTable).focus()
            self._refresh()
            return
        if isinstance(self.screen, CredentialDetail):
            self.pop_screen()
            return

    def action_toggle_theme(self):
        self.CURRENT_MODE = "light" if self.CURRENT_MODE == "dark" else "dark"
        self.css = DARK_CSS
        self.refresh_css()
        self._update_status_bar()

    def action_cycle_focus(self):
        widgets = [
            self.query_one("#creds-table", DataTable),
            self.query_one("#audit-log", RichLog),
        ]
        for i, w in enumerate(widgets):
            if w.has_focus:
                nxt = widgets[(i + 1) % len(widgets)]
                nxt.focus()
                return
        widgets[0].focus()

    def on_input_changed(self, event):
        if event.input.id == "search-bar":
            self._filter = event.value.strip()
            self._refresh()

    def on_data_table_row_selected(self, event):
        self._selected_row = event.value

    def action_show_detail(self):
        table = self.query_one("#creds-table", DataTable)
        row = table.cursor_row
        if row is None:
            return
        try:
            cells = table.get_row_at(row)
            name = re.sub(r"\[.*?\]", "", str(cells[0])).strip()
            self.push_screen(CredentialDetail(name, str(cells[1]), str(cells[2]), str(cells[3])))
        except Exception:
            pass

    def _update_status_bar(self, summary=""):
        bar = self.query_one("#status-bar", Static)
        mode = "DARK" if self.CURRENT_MODE == "dark" else "LIGHT"
        text = f"  [{mode}]  [q]uit  [r]efresh  [/]search  [t]heme  [d]etail  [Tab]focus"
        if summary:
            text += f"  |  {summary}"
        bar.update(text)

    @work(exclusive=True)
    async def _refresh(self) -> None:
        self._poll_count += 1
        vault = sv.load_vault()
        creds = vault.get("credentials", {})
        scanned = sv.SCANNED_FLAG.exists()

        header = self.query_one(Header)
        ts = datetime.now(timezone.utc).strftime("%H:%M:%S")
        total = len(creds)
        n_chat = sum(1 for k in creds if k.startswith("chat."))
        n_file = total - n_chat
        if scanned:
            header.tab = f"\U0001f512  {total} creds ({n_file} file, {n_chat} chat)  {ts}"
        else:
            header.tab = f"\u26a0  {total} creds  NOT SCANNED  {ts}"

        table = self.query_one("#creds-table", DataTable)
        table.clear()
        filtered = 0
        for name in sorted(creds.keys()):
            if self._filter and self._filter.lower() not in name.lower():
                continue
            filtered += 1
            is_chat = name.startswith("chat.")
            ctype = "chat" if is_chat else "file"
            encrypted = creds[name]
            length = len(encrypted)
            source = "chat" if is_chat else name.split(".")[0]
            table.add_row(name, ctype, str(length), source)

        status_text = self.query_one("#file-status", Static)
        files_info = vault.get("files", {})
        redacted = []
        for fp_str in files_info:
            p = Path(fp_str)
            if p.exists():
                c = p.read_text(encoding="utf-8", errors="replace")
                if "[REDACTED:" in c:
                    redacted.append(p.name)
        last_scan = vault.get("last_scanned", "never")
        status_text.update(
            f"[bold]File Status[/]\n"
            f"  Backed up: {len(files_info)}  |  "
            f"Redacted now: {len(redacted)}  |  "
            f"Last scan: {last_scan[:19] if last_scan != 'never' else 'never'}"
        )

        if self.show_audit:
            log = self.query_one("#audit-log", RichLog)
            if self._poll_count <= 1:
                log.clear()
                log.write("[bold]Audit Log[/]")
            audit = sv.load_audit()
            log.clear()
            log.write("[bold]Audit Log (last 15)[/]")
            for entry in audit[-15:]:
                ts_str = entry.get("timestamp", "?")[:19]
                action = entry.get("action", "?")
                cred = entry.get("credential", "?")
                purpose = entry.get("purpose", "")
                log.write(f"  {ts_str}  {action:>8s}  {cred}  {purpose}")

        summary = f"{filtered}/{total} shown"
        self._update_status_bar(summary)


def run_tui(interval=2.0, show_audit=True):
    app = VaultMonitor(interval=interval, show_audit=show_audit)
    app.css = DARK_CSS
    app.run()


if __name__ == "__main__":
    run_tui()
