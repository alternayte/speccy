# A bundle carries the files its doc references

## What it does
A bundle takes the files its main doc points at. A relative link or an image that resolves to a file in the source comes into the bundle, keeps the path the doc wrote, and renders, exports and travels in the build packet. Speccy follows the references of each markdown file it takes, until nothing new appears. A carried file is read-only: Speccy never writes it back. The broken link finding then reports only a link that is really broken.

## Decisions
- The bundle takes the file, and the checks are not taught to look outside it — the bundle is the unit the preview, the zip, the HTML report and the build packet all read, so a doc whose references sit outside it is incomplete for every one of them.
- A reference is a relative link or an image source that resolves to a file inside the source — an absolute path, a URL and an anchor stay as they are, because none of them names a file of this repo.
- A target that is itself a mapped or adopted doc is not taken — it is another bundle, two bundles must not hold one text, and the link rules already relate them.
- A target above the bundle's own folder is not taken — a bundle path may hold no ".." segment (source.CleanPath), so there is no path under which the bundle could hold it, and the doc's link must not be rewritten. The link keeps today's finding, which now says the truth.
- For a one-doc bundle the folder is the one that holds the doc — the bundle is that doc, and a folder of "the doc" would admit nothing.
- Speccy follows references to closure, through every markdown file it takes — stopping at one step would take a reference doc and leave the diagram that doc shows broken, which is the same fault one level down.
- A file already in the bundle is not taken twice, so a cycle ends by itself.
- The admin's largest-bundle limit bounds the walk. When it stops, the scan reports a problem that names the file it did not take — that limit already protects a folder bundle, and a second cap in files is a number nobody can reason about.
- A carried file is read-only. The explorer does not edit, rename or delete it, and a publish never writes it — two docs can reference one image, so two bundles hold it, and a publish from either would overwrite the other with nobody the wiser.
- A carried file keeps the path the doc wrote, relative to the bundle — the link in the doc then resolves with no rewrite.
- The explorer marks a carried file and names the doc whose reference brought it — a person must know which files Speccy took and will not write back.
- A carried file is an asset for the review: the rubric, the grounding stage and the divergence readers see it, under the text asset cap that exists — a requirement in a referenced table is a requirement the readers must see.
- A change to a carried file makes a new bundle version, and the old verdict goes stale — a run pins the version, so anything a reader saw belongs in it.
- Every bundle follows this rule, not only the one-doc kind — the fault is the same for a folder bundle whose doc points one folder up, and two rules for one fault would make the result depend on how a person imported.
- A folder bundle still takes its own folder. References only add what lies outside it.
- Local mode copies nothing onto disk. The carried file enters the version snapshot in the store — the file on disk stays the one writable copy.

## Out
- No write of a carried file: no edit, no rename, no delete, no publish.
- No copy of a mapped or adopted doc into another bundle.
- No reach above the bundle's folder, and no follow of a URL.
- No new size limit: the bundle limit stands.
- No change to the assets folder convention. `<name>.assets/` still comes with a one-doc bundle.
- No rewrite of a link in the doc.

## How I know it works
- A one-doc source on a doc that writes `![flow](images/flow.png)` makes a bundle holding `build-report.md` and `images/flow.png`. The preview shows the image, and lint reports no broken link.
- The same doc links `./limits.md`, a file that no mapping covers. The bundle holds `limits.md`, and a table inside it reaches the rubric.
- `limits.md` itself writes `![](images/limit.png)`. That image is in the bundle too.
- The doc links `./other-spec.md`, which a mapping covers. The bundle does not hold it, and the link is not a broken link finding.
- The doc links `../../README.md`, above the source. The bundle does not hold it, and the finding says the link points outside the bundle.
- The explorer marks `images/flow.png` as carried and names the doc that references it. It offers no rename and no delete, and Publish sends no change for it.
- A person edits the doc and publishes. The pull request holds the doc only.
- The image changes in the repo. The next sync makes a new bundle version, and the verdict for the old version reads stale.
- A folder bundle whose doc links `../shared/glossary.md` does not hold it, and the finding says the link points outside the bundle.
- A folder bundle whose doc links `sub/glossary.md` holds that file.
- A doc that references more text than the bundle limit allows gets a scan problem naming the first file Speccy did not take.
- `just verify` passes.
