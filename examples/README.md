# Example MCP configs

Ready-to-paste config for the popular MCP clients. The server block is the same
everywhere; only the **root key** and the **file location** change. Two files
cover the standard clients:

- [`mcp-servers.json`](mcp-servers.json) - the `{"mcpServers": {...}}` shape used
  by Cursor, Claude Desktop, Claude Code, Windsurf, opencode, Cline, and any other
  Claude-Desktop-style client.
- [`vscode.mcp.json`](vscode.mcp.json) - VS Code / GitHub Copilot uses `"servers"`
  as the root key instead of `"mcpServers"` (same block, different key).
- [`hermes.yaml`](hermes.yaml) - Hermes Agent (NousResearch) uses a YAML file.

> If your client reports `spawn agent-browser ENOENT`, it can't find the binary on
> its PATH. Use the absolute path in `command`: `$(go env GOPATH)/bin/agent-browser`
> (append `.exe` on Windows).

## Where each client reads its config

| Client | Root key | Config location | How to apply |
|---|---|---|---|
| Cursor | `mcpServers` | `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (project) | paste `mcp-servers.json`; reopen the project |
| Claude Desktop | `mcpServers` | macOS `~/Library/Application Support/Claude/claude_desktop_config.json` · Windows `%APPDATA%\Claude\claude_desktop_config.json` | paste `mcp-servers.json`; **fully quit + reopen** Claude |
| Claude Code | `mcpServers` | `~/.claude/settings.json` (user) or `.mcp.json` (project, team-shareable) | `claude mcp add agent-browser -- agent-browser mcp` |
| Windsurf | `mcpServers` | `~/.windsurf/mcp.json` (global) or `.windsurf/mcp.json` (project) | paste `mcp-servers.json` |
| VS Code (Copilot) | `servers` | `.vscode/mcp.json` (workspace) or the user `mcp.json` (Command Palette → "MCP: Open User Configuration") | paste `vscode.mcp.json`; enable `"chat.mcp.enabled": true` in settings |
| opencode | `mcpServers` | `~/.config/opencode/opencode.json` | paste `mcp-servers.json` |
| Hermes Agent | `mcp_servers` (YAML) | `~/.hermes/config.yaml` | paste `hermes.yaml`; run `/reload-mcp` |
| OpenClaw | (CLI registry, no JSON file) | `openclaw mcp add ...` | see below |
| pi | (no MCP) | (n/a) | see note below |

## OpenClaw

OpenClaw doesn't use a Claude-Desktop-style JSON file; it keeps an MCP
client-side registry you populate from the CLI, then projects those servers into
its runtimes. Register + verify:

```sh
openclaw mcp add agent-browser --command agent-browser --arg mcp
openclaw mcp doctor --probe agent-browser
```

(OpenClaw is deliberately token-stingy about MCP tool defs (same philosophy as
agent-browser), so it's a natural fit.)

## pi

[pi](https://github.com/earendil-works/pi-coding-agent) doesn't consume MCP
servers by design; it uses CLI tools, skills, and extensions instead (to avoid
the tool-def token tax, the exact problem agent-browser exists to solve). So
there's no MCP config to paste. To drive a browser from pi, use a pi extension
(a `.ts` file under `~/.pi/agent/extensions/`); a Playwright-based
`agent-browser` extension already exists there as a reference. A pi extension
that shells out to this Go binary is possible, but it's an extension, not a
config-file setup.

## Notes

- **8 tools** load by default: nav, see (brief/refs/text/outline/full/shot),
  act (click/fill/select/hover/press/upload by intent/ref/selector + optional
  wait), js (run JS with a helper API -> clean JSON), find (by role/text or
  selector; `selectors=true` for both), tabs, history, session (reset/clear).
  v3 collapsed the v2 22-tool surface into a few god-tier composable tools.
- **Persistence**: by default agent-browser keeps a Chrome profile at
  `<os config dir>/agent-browser` so logins/cookies survive restarts - the agent
  doesn't re-login every run. One profile per process (Chrome locks it); for
  concurrent clients, pass distinct `--user-data-dir` args. Use `--no-persist`
  for a throwaway profile.
- **js** is on by default (add `--no-eval` to disable). **Stealth** is on by
  default (add `--no-stealth` to disable). See the root [README](../README.md)
  for the full flag list.
