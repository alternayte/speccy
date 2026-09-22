# Delete a bundle, and keep profiles

## What it does
A person deletes a bundle from its More menu. A bundle whose text Speccy holds goes for good, with everything that hangs off it. A bundle that a local folder or a GitHub source keeps making is not deleted: the control names the real act and opens it. On the profile side, an admin creates a profile, starts one from an existing profile, and deletes one. Every profile page lists its versions, shows a side-by-side diff between any two, and rolls an old one forward.

## Decisions
- A delete removes what Speccy holds, and never a file on disk or in a repo — Speccy does not run git on the person's machine (DEC-018), and a repo is other people's tree.
- A delete is permanent: the bundle, its versions and blobs, its review runs and findings, its threads, waivers, handoffs and verification runs, and the event streams behind the threads, waivers and bundle status — a person who clicks Delete means gone, and a second reversible state is the ceremony that made them ask.
- The person types the bundle slug to confirm — the act destroys review history that nothing else holds.
- `archived_at` keeps its meaning: the mark Speccy sets by itself when a folder or a doc disappears — Speccy decided it, so Speccy can undo it.
- A `db` bundle deletes outright.
- A GitHub-backed bundle opens the source that holds it, and offers to remove that source. The offer names how many other bundles go with it — the source makes the bundle again on the next sync, so removing the bundle alone is a lie.
- A local-folder bundle offers nothing destructive. The control says the folder on disk is the truth — the next scan reads that folder again.
- An author of the bundle or an admin deletes it. A member who is not an author cannot, and a guest never can — `bundle_author` already decides who may not approve their own bundle.
- An approval does not protect a bundle — the verdict and the approvals describe a doc that is going away with them.
- A built-in profile cannot be deleted — `prd` and `sdd` ship in the binary, and a workspace edit of one is a new version of it.
- A profile delete is refused while a bundle names its key. The problem says how many bundles and links to them — a bundle whose profile key resolves to nothing has no rubric, no template and no verdict rule.
- An old review run keeps its profile version and goes on reading it after the profile is deleted — a run states what it judged by, and that statement must stand.
- New profile takes an optional profile to start from, and prefills its YAML and template — `createProfile` already takes a whole profile, so a copy needs no new call.
- A rollback writes a new version whose YAML and template equal the chosen one. It moves no pointer and reuses no number — a run pins its profile version, and a number that changed meaning would rewrite what an old run was judged by (REQ-012).
- The version row of a rollback says which version it repeats — the history must say what happened.
- The diff runs on the server and returns line operations, as the bundle version diff does — one diff, and the browser already draws those operations side by side.
- A profile diff compares any two versions, and opens on the version before the one selected.
- An admin creates and deletes a profile. A maintainer of that profile, or an admin, rolls one back — a rollback is an edit, and `updateProfile` already has that gate.

## Out
- No change to a file on disk, and no commit to a repo.
- No trash, no restore, and no screen of archived bundles: the bundle list already hides them.
- No delete of a review run, a thread or a waiver on its own.
- No change to a verdict when a profile gains a version, including a rollback.
- No bulk delete of bundles or profiles.
- No export before a delete.

## How I know it works
- A bundle made in the app shows Delete in its More menu. The dialog asks for the slug, and refuses a wrong one.
- After the delete, the bundle is gone from the list and from the API, its review runs return 404, and its threads and waivers are gone from the inbox.
- A bundle from a GitHub source shows Delete, and the dialog names the source and the number of bundles it holds. It removes the source, and the bundles leave the list.
- A bundle from a local folder shows Delete, and the dialog says the folder is the truth and offers no destructive act.
- A member who is not an author gets a problem that says an author or an admin deletes a bundle. A guest sees no Delete.
- The Profiles screen has New profile. With "start from sdd" the form opens with the SDD YAML and template, and a new key saves a separate profile at version 1.
- A profile page shows Delete. On a built-in profile the control says it ships in the binary. On a profile that two bundles use, the problem names the two and links to them.
- The profile page lists every version with its number, who saved it and when. Selecting two shows the YAML diff and the template diff side by side.
- Roll back on v2 of a v4 profile writes v5 with the text of v2, and the row says it repeats v2. A review run from v3 still reports that it used v3.
- `just verify` passes.
