# The CLI, the TUI, and the MCP server

Every command works on the folder that holds `.speccy.yaml`, from the current folder up, or else on the current folder.

## The commands

| Command | What it does |
|---|---|
| `speccy` | Local mode, and it opens your browser. |
| `speccy serve --dir <folder>` | Local mode without the browser. `--hosted` runs hosted mode. |
| `speccy init` | Writes `.speccy.yaml` and ignores `.speccy/state/`. |
| `speccy review <path…>` | Reviews bundles. `--format text\|json\|md`, `--summary`, `--stages`, `--enforcement`, `--server`. |
| `speccy action` | The GitHub Action: reviews what a pull request changes and comments on it. |
| `speccy tui` | The terminal UI. |
| `speccy mcp` | The MCP server over stdio. |
| `speccy profile validate <file>` | Checks a profile file. |
| `speccy export <path> --format zip\|html` | Writes a .zip of a bundle, or its HTML report. |
| `speccy admin invite --role admin\|member` | Prints an invite link (hosted mode). |

Exit codes of `speccy review`: 0 Build Ready or advisory, 1 Not Build Ready in blocking mode, 2 a usage error, 3 a run error.

```sh
speccy review docs/specs/* --summary
speccy review docs/specs/payments --format md > review.md
```

## The terminal UI

`speccy tui` lists the bundles, runs a review, shows the verdict and the findings, steps through the tour, and opens a file in `$EDITOR` at the finding.

![The TUI bundle list, with the preview of the bundle under the cursor](images/tui-list.png)

Press `enter` to open a bundle. The findings table shows the level, the file and line, and the message, with the selected finding below it.

![The TUI bundle screen: the verdict, the findings, and the selected finding](images/tui-bundle.png)

| Key | What it does |
|---|---|
| `j` `k`, `↓` `↑` | Move |
| `enter` | Open the bundle, or the finding in `$EDITOR` |
| `e` | Open the file in `$EDITOR` at the finding |
| `r` | Run a full review |
| `t` | Open the tour |
| `esc`, `h` | Go back |
| `?` | The full key list |
| `q`, `ctrl+c` | Quit |

The TUI works from 80 by 24 cells. Below 96 columns the list drops the profile and score columns.

## Connected mode

`speccy review --server https://speccy.example` sends the files to a Speccy server, which reviews them with its models and its linked docs. The server changes no bundle. It keeps each review for 90 days and prints a link to its report, which members of the workspace can open. The token is in `SPECCY_TOKEN`.

## MCP

Add Speccy to a coding agent as an MCP server:

```sh
claude mcp add speccy -- speccy mcp
```

The agent can then list bundles, review a doc it is writing before it saves, read the findings, and apply a certain fix. In hosted mode the same tools are at `/mcp`, with a personal API token.
