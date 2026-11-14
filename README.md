# factory

A cozy factory game for your terminal, drawn in real pixel art when you play it in [Ghostty](https://ghostty.org).
Mine ore, smelt it, craft parts and sell them. Nothing attacks you and nothing breaks; the factory keeps earning while you're away.
It's made to sit in a split next to your AI agents, and they can send you messages that show up in the game.

Inspired by [Terminactory](https://gitlab.com/snipedbywifi/terminactory).

![A small factory](docs/screenshot.png)

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/thesnarkitecht/factory/main/install.sh | sh
```

This puts `factory` in `~/.local/bin` (set `FACTORY_BIN_DIR` to change that). It downloads a prebuilt binary for macOS or Linux, or builds it with Go if no binary is published. To build from source yourself: `go install github.com/thesnarkitecht/factory@latest`.

## Ghostty native

- **Pixel graphics** through the Kitty graphics protocol, drawn at your screen's real pixel size, so sprites stay sharp on Retina displays. When you play locally, frames go through a temp file instead of the terminal stream.
- **Synchronized output** (mode 2026), so frames never tear.
- **Tab progress bar** (OSC 9;4) that fills toward the $50,000 goal, and **desktop notifications** (OSC 9) when an agent messages you.
- **Mouse**: click to build, drag to lay belts, right-click to remove, scroll to zoom.

It works in other terminals too. At startup it asks the terminal what it supports. Kitty, and any other terminal that answers the graphics probe, gets the same pixel graphics. Everything else, including tmux, gets a truecolor half-block renderer. `factory --blocks` forces the half-block renderer.

## Playing

| Key | Does |
| --- | --- |
| arrows / wasd / hjkl | move the cursor |
| Shift+arrows / WASD | lay a line of the selected building (belts!) |
| `1`–`5` | pick Belt, Drill, Furnace, Assembler, Trade post (`0` to inspect only) |
| Space / Enter / click | build |
| `x` / right-click | remove (full refund) |
| `r` `R` | rotate |
| `f` | change an assembler's recipe |
| `+` `-` / scroll | zoom |
| `c` | center the view |
| `p` | pause |
| `?` | help |
| `n` | clear messages |
| Ctrl+S / `q` | save / save and quit |

How it works:

- **Drills** mine the ore under them and push it out of their yellow side.
- **Belts** carry the ore.
- **Furnaces** smelt ore into plates and stone into bricks. Each coal they burn covers 4 smelts.
- **Assemblers** make gears, wire, circuits and engines. They unlock as your total earnings grow.
- **Trade posts** buy anything you deliver to them.

A machine that's stuck shows a blinking red dot. Move the cursor onto it to see why in the sidebar: `NO ORE`, `NEED COAL`, `NEED INPUT` or `FULL`.

The game autosaves to `~/.factory/save.json` every 30 seconds and when you quit. `factory --new` starts over and keeps the old save as `save.json.bak`.

## Agent messages

Anything that can run a shell command can message you in the game:

```bash
factory msg --from my-agent "Tests fixed\n- 3 flaky tests"
echo "done" | factory msg
```

You can also drop a file into `~/.factory/inbox/`.

**Claude Code**: add these hooks to `~/.claude/settings.json`. You'll get the agent's last reply when it finishes, and a "needs you" message when it's waiting on you.

```json
{
  "hooks": {
    "Stop": [{ "hooks": [{ "type": "command", "command": "factory hook" }] }],
    "Notification": [{ "hooks": [{ "type": "command", "command": "factory hook" }] }]
  }
}
```

## Development

```bash
go run .          # play from source
go test ./...     # simulation, input parsing, hooks, and a rendered frame
FACTORY_PNG=/tmp/f.png go test -run RenderPNG   # look at the sprites
```

- `sim.go`: the rules. 20 ticks a second, no drawing.
- `render.go`: procedural pixel-art sprites, cached for each zoom level.
- `term.go`: input parsing, terminal capability probe, Kitty graphics and the half-block renderer.
- `main.go`: the app loop, UI panels and CLI.

Releases: push a `v*` tag. CI builds the macOS and Linux binaries that the install script downloads.
