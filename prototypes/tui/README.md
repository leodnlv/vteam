# vteam TUI prototype

A full-screen, keyboard-driven TUI for vteam. It is a throwaway prototype: it is not part of the build, lint, or tests, and it imports straight from `../../src`.

## Run

```sh
npx tsx prototypes/tui/main.ts                       # uses the current directory
npx tsx prototypes/tui/main.ts --cwd ~/code/my-app   # any project with a vteam/ directory
```

Type-check it with `npx tsc -p prototypes/tui/tsconfig.json`.

### Browser mock

`index.html` is a static, self-contained mock of the same TUI for clicking around: open it in a browser (`open prototypes/tui/index.html`). It uses sample data based on real run logs, not your project. Tabs, agents, run buttons, and the footer hints are clickable, and the same vim keys work. The `coder` log "streams" a few more lines every 2.5s to show follow mode.

## Layout

```
vteam ▸  1 Overview   2 Agents   3 Info            my-app (aven) · label vteam · ⎇ main
────────────────────────────────────────────────────────────────────────────────────
 body (tab content)
────────────────────────────────────────────────────────────────────────────────────
j/k scroll  gg/G top/bottom  r refresh  1-3 tabs  ? help  q quit     updated 10:42:01
```

- **Top right**: aven project (`tasks.project`); falls back to the directory name when `vteam.config.json` fails to load. Also shows the task label and the git branch.
- **Overview**: the output of `renderStatus` (the same thing `vteam dashboard` shows), refreshed every 2s, scrollable.
- **Agents**: a sidebar with every agent, plus `merge-ensurer` and any agent that has a `vteam/.logs/<name>/` directory. `●` means it is running (it has a live lock). The main pane shows the agent's newest run log, tails it while it grows, and colors it by line glyph. `p` / `n` step through older runs.
- **Info**: `vteam.config.json`, scheduler state, and each agent's `agent.json` settings.
- **Footer**: hints for the current tab and focus. `?` opens the full key list.

## Keys

| Keys | Action |
|---|---|
| `1` `2` `3`, `Tab` / `Shift-Tab` | switch tabs |
| `j` `k` / `↓` `↑` | scroll; in the Agents sidebar, select an agent |
| `Ctrl-d` `Ctrl-u` / `PgDn` `PgUp` | half page down / up |
| `gg` / `G` | top / bottom (`G` in a log turns follow back on) |
| `l` `Enter` `→` / `h` `Esc` `←` | focus the log / the sidebar |
| `p` / `n` | previous (older) / next (newer) run log |
| `f` | toggle follow |
| `w` | toggle line wrap |
| `r` | refresh now |
| `?` | help |
| `q` `Ctrl-C` | quit |

## Files

- `main.ts`: app state, refresh timers, key handling
- `views.ts`: rendering for the header, footer, and each tab
- `data.ts`: project info, agent entries (config, live lock, log files), cached log reads
- `index.html`: static browser mock with sample data
- `screen.ts`: raw ANSI helpers (alternate screen, ANSI-aware truncation, wrapping, key parsing)

## Known gaps

- It uses no TUI library, only raw ANSI. Wide characters (emoji) are counted as one column, so lines that contain them can be off by one.
- It re-reads the whole selected log file when it changes. Fine for normal logs, slow for huge ones.
- No mouse, no search (`/`), and no actions (run an agent, clean, ensure-merge). These would come next.
- `renderStatus` calls aven every 2s (one `aven show` per task), the same as `vteam dashboard`.
