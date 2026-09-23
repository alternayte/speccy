# A folder of spec docs: adopt, rail, fixes and wrapped frontmatter

Issues: #73, #74, #75, #76. #66 and #67 are fixed on main by #70 and #72.

## What it does
A folder of docs that name no type stays in the list until the person decides on each doc, and one click accepts all picked types. The findings rail always shows the findings of the doc on screen, and a finding click opens the doc that owns it. The suggested fix for `links.has-upstream` writes a correct link. Speccy checks every suggested fix for a lint finding before it offers the fix. Speccy reads frontmatter inside an HTML comment, writes it back in the same form, and says when it cannot read a frontmatter block.

## Decisions
- The scan keeps a skipped doc in `Skipped` also when it is an asset of a bundle. It stays an asset until a person accepts a type or marks it Not a spec (#73). This way an Accept on one doc cannot remove the other docs from the list.
- The list of docs that name no type gets "Accept all picked types". It sends every row that has a picked type in one adopt request, in local mode and for a source. The order of the clicks then does not matter.
- The file tree gets no control that makes an asset a spec doc. The adopt path is the only way to do this.
- The findings query key holds the doc ID. `keepPreviousData` applies only while the doc stays the same (#74). A doc with no run shows no findings, not the findings of the previous doc.
- `openAnchor` compares the anchor file with the spec docs of the bundle. For another spec doc, it calls `openDoc` first and then scrolls. The offsets of one doc never move the editor of another doc.
- The rail shows the findings of one doc only, with no combined view for the bundle. Speccy never combines the spec docs of a bundle.
- Suggest fix on `links.has-upstream` calls no model. It offers the upstream docs that the check can accept as choices, and the person picks one (#75). The model cannot then invent a link format.
- On accept, Speccy writes the picked link with `source.AddLink` into a doc Speccy owns. For a repo source, it stores an adopted link. Speccy writes no file in a repo it does not own.
- For every other lint finding, the fix prompt gives the exact frontmatter format of `links`. `SuggestFix` lints the patched text. If the check still fails, Speccy retries once with the lint message. If it still fails after that, Speccy returns "fix_failed" with the lint message. Speccy never offers a fix that it knows does not work.
- After an accept of a lint finding's fix, the rail shows the lint result of the new version: "Fixed" or "Still fails: <message>". "Run the review again to check it" stays for AI findings only. A lint finding is checked in the moment, so the person needs no second step.
- `SplitFrontmatter` accepts an optional `<!--` line before the opening `---` and an optional `-->` line after the closing `---`, with blank lines around them (#76). All readers use it, so render, anchors, lint and scan all read the block.
- `SplitFrontmatter` also returns the form of the block: plain or wrapped, and YAML or JSON with its indent. `SetKeys`, `AddLink`, `AddTypeLine` and `fromTemplate` write the block back in the same form and key order. A wiki then keeps hiding the block after Speccy writes to it.
- A new built-in check `frontmatter.readable`, with a default level of MUST, fails when a frontmatter block does not parse. It also fails when a `<!-- -->` block that holds a `---` block is in a form or position Speccy does not read, or when `links` is not a list of `kind` and `target` pairs with a known kind. The finding names the problem, and for `links` it gives the expected format. The frontmatter holds the type, the size and the links, so an unread block changes the verdict.
- When `frontmatter.readable` fails, the `links.has-upstream` message says that Speccy cannot read the frontmatter. It does not say "no implements link". The author then fixes the cause, not the symptom.
- `.speccy.yaml` gets no map from a template's key names to Speccy's keys. Only one template needs it.

## Out
- No combined findings view for a bundle, and no "no doc selected" state.
- No file tree control to make an asset a spec doc.
- No `frontmatter.keys` map.
- No model-written fix for `links.has-upstream`.

## How I know it works
- A GitHub folder with `PRD - X.md` and `SDD - X.md` and no type: pick `prd` and `sdd`, then Accept the PRD. The SDD stays in the list. Accept it, and the bundle shows two spec docs, each with a profile chip.
- The same folder with both types picked: one click on "Accept all picked types" makes the bundle with both spec docs.
- A bundle with a reviewed PRD and an unreviewed SDD: open the PRD, then click the SDD. The rail shows no PRD findings, and the count and the list agree.
- A finding with an anchor in the PRD, clicked while the SDD is open, opens the PRD and highlights the quoted text.
- On an SDD with no link, Suggest fix on `links.has-upstream` lists the PRDs. Pick one and accept. The rail shows "Fixed", and the frontmatter holds `links: - kind: implements, target: …`.
- A doc whose frontmatter is JSON inside `<!-- -->` shows its type and title. After an accepted link fix, the file still starts with `<!--`, the block is still JSON with the same indent, and the link is in it.
- A doc with `"links": {"implements": ["PRD - X.md"]}` gets a `frontmatter.readable` finding that shows the expected `links` format.
