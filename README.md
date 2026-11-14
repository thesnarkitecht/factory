# factory

A pixel-art tower defense game for your terminal. It looks best in [Ghostty](https://ghostty.org).

Waves of scrap bots ride a conveyor toward your factory. Place towers along the conveyor and upgrade them to stop the bots. Your money comes in on a timer, like the classic tower defense battle games: spend some of it sending bots to your rival's conveyor, and your pay goes up. The rival does the same to you. Whichever factory falls first loses.

It's made to sit in a split next to your AI agents, and they can send you messages that show up in the game.

![Your board with the rival's below it](docs/screenshot.png)

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/thesnarkitecht/factory/main/install.sh | sh
```

This puts `factory` in `~/.local/bin` (set `FACTORY_BIN_DIR` to change that). It downloads a prebuilt binary for macOS or Linux. If there is none, it builds from source, fetching a temporary copy of Go if you do not have one (no sudo or Homebrew needed). To build from source yourself: `go install github.com/thesnarkitecht/factory@latest`.

## Ghostty native

- **Pixel graphics** through the Kitty graphics protocol, drawn at your screen's real pixel size, so sprites stay sharp on Retina displays. When you play locally, frames go through a temp file instead of the terminal stream.
- **Synchronized output** (mode 2026), so frames never tear.
- **Tab progress bar** (OSC 9;4) that shows your lives and turns red when they run low, and **desktop notifications** (OSC 9) when an agent messages you.
- **Mouse**: the cursor follows your pointer. Click to build or upgrade, and right-click to stop placing.

It works in other terminals too. At startup it asks the terminal what it supports. Kitty, and any other terminal that answers the graphics probe, gets the same pixel graphics. Everything else, including tmux, gets a truecolor half-block renderer. `factory --blocks` forces the half-block renderer.

## Playing

| Key | Does |
| --- | --- |
| arrows / wasd / mouse | move the cursor |
| `1`–`4` | pick a tower (press again to stop placing) |
| Space / Enter / click | build, or upgrade the tower under the cursor |
| `u` / `x` | upgrade / sell (75% back) |
| `z` `c` `v` `b` | send bots to the rival; each send raises your pay |
| `f` / `p` | fast-forward / pause |
| `?` / `q` | help / quit |

You can also click the build and send rows in the sidebar.

**Towers** (each upgrades to level 4):

| Tower | Cost | Does |
| --- | --- | --- |
| Gun turret | $150 | fast single shots |
| Cannon | $320 | blasts a group |
| Tesla coil | $450 | a bolt that arcs through 3 bots, and more as it levels |
| Frost vent | $220 | slows every bot in range, and chips damage from level 3 |

**Bots:**
- Scrap bots come in swarms.
- Bolts are fast.
- Brutes are tough.
- Hulks are slow armored tanks that cost you 20 lives if they get through.

A new wave comes every 20 seconds, and each wave is bigger. You get paid every 6 seconds, and killing bots pays a bounty too. Bigger sends unlock in later rounds.

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
go test ./...     # simulation, rival AI, sprites, input parsing, hooks, and a rendered frame
FACTORY_PNG=/tmp/f.png go test -run RenderPNG   # look at a frame
```

- `td.go`: the rules: path, towers, bots, waves, sends, pay and the rival AI. 20 ticks a second, no drawing.
- `sprites.go`: hand-drawn 16×16 pixel art. Edit the letters to redraw a unit.
- `render.go`: draws the sprites (rotated, anti-aliased, and cached for each tile size), the conveyors and the effects.
- `term.go`: input parsing, the terminal capability probe, Kitty graphics and the half-block renderer.
- `app.go`, `main.go`: the game loop, the UI panels and the CLI.

Releases: push a `v*` tag. CI then builds the macOS and Linux binaries that the install script downloads.
