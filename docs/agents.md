# Run SHELFCODE from your AI assistant (MCP)

SHELFCODE has a built-in [MCP](https://modelcontextprotocol.io) server. Connect an AI assistant (Claude Code, Claude Desktop, Cursor, VS Code Copilot, Windsurf, Gemini CLI, Codex, or any MCP app) and run your inventory by asking. You don't need to open menus or fill in forms.

> "Add 12 black Northline tees, S to XL, $19.99, and print hang tags for all of them."
> "What phones under $300 do we have in grade A?"
> "Sold the iPhone with IMEI 356789104455661 for $420."
> "Make me 50 blank stickers on Avery 5160 sheets."
> "Undo the last price change on the denim jacket."

While the assistant works, your open SHELFCODE page follows along. For example, when it prepares labels, the filled-in print sheet opens on your screen and you just press **Print**.

## Connect (one click)

**On the PC running SHELFCODE:** open **Settings → AI agents** and click **Connect** next to your assistant, or **Connect all**. SHELFCODE writes the assistant's settings itself, so there's nothing to copy or paste. Then start a new session in that assistant. You can also type `agents` in the SHELFCODE window to connect every assistant it finds.

| Assistant | What SHELFCODE changes |
|---|---|
| Claude Code | runs `claude mcp add --scope user shelfcode -- <SHELFCODE> mcp` (or edits `~/.claude.json`) |
| Claude Desktop | `claude_desktop_config.json` → `mcpServers.shelfcode` |
| Cursor | `~/.cursor/mcp.json` |
| VS Code (Copilot) | user `mcp.json` → `servers.shelfcode` |
| Windsurf | `~/.codeium/windsurf/mcp_config.json` |
| Gemini CLI | `~/.gemini/settings.json` |
| Codex | `~/.codex/config.toml` → `[mcp_servers.shelfcode]` |

Before changing any of these files for the first time, SHELFCODE saves a copy next to it (`….shelfcode-backup`). **Disconnect** removes only the SHELFCODE entry.

The assistant starts `SHELFCODE mcp` by itself. If SHELFCODE is already open, the request goes through it, so your screen follows along. If SHELFCODE is closed, the assistant still works directly on your data.

**From another computer, or with SHELFCODE on a server (Linux, ZimaOS, Docker):** use the **Connect your AI assistant** section in Settings → AI agents. It has **Add to Cursor** and **Add to VS Code** buttons that install with one click, plus ready-made commands for Claude Code and Gemini CLI. These connect to `http://<server>:8765/mcp` with a private token. Example for Claude Code:

```bash
claude mcp add --transport http shelfcode http://192.168.1.20:8765/mcp --header "Authorization: Bearer sc_…"
```

The token works like a password. **New token** in Settings → AI agents cancels the old one.

## What the assistant can do

| Tool | |
|---|---|
| `get_store_overview` | categories with their fields, statuses, label designs, totals, and your **store notes** |
| `search_items`, `get_item` | find by any words or exact field values. Look up by any code (IMEI, UPC…). |
| `create_items` | add one or many items. Codes are generated automatically. |
| `update_item` | change prices, fields, status, location… |
| `make_variants` | sizes × colours, each with its own code |
| `adjust_stock` | sold, received, returned, damaged, counted |
| `create_category`, `add_field` | new categories from a template or with custom fields (including scannable ones like IMEI) |
| `prepare_labels` | a print sheet with any mix of items, copies and blank codes, opened on your screen |
| `item_history`, `revert_change` | see every change to an item, and undo one |
| `delete_items`, `restore_items` | Trash and restore |
| `inventory_report` | stock value, low stock, sales, top sellers |
| `remember_preference` | saves "always…" / "from now on…" rules to your store notes |
| `open_in_app` | show an item, a search or a page on your screen |

## Store notes

**Settings → AI agents → Store notes** holds the rules your assistant always follows, such as "Prices end in .99" or "Tag new arrivals with *new*". The assistant reads them before every task and adds new ones when you tell it "always…" or "from now on…".

## Safety

Every change goes through the same checks as the app: unique codes, required fields, history. Deleting moves items to the Trash, and any edit can be undone from the item's history. On another computer, an assistant can only connect with the token. On a server, keep the token private.
