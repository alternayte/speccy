# Grounding reads the bundle's files

## What it does
The grounding stage checks each claim against the text that the review run already holds, before any search. A file of the bundle or a linked spec doc that states the claim makes it verified, with the path as its source. A file of the bundle that states the opposite gives a SHOULD finding that names the file and shows both quotes. A claim that no file settles goes on as in v0.22.0. At the default settings the verdict of a doc does not change.

## Decisions
- A source is an asset or a carried file of the bundle. Speccy reads no other doc under the root folder. — The author chooses the evidence, and a doc about another thing with the same name gave the false finding of #121.
- Speccy checks every claim against the files first, internal and external. — The author put the file in the bundle as evidence, and a claim that a file settles needs no call with web search.
- A claim that a file confirms or contradicts gets no web search and no MCP search. — Two sources that disagree give two findings for one claim.
- A claim that no file settles goes to the web search or the MCP connection for an external claim, and to the MCP connection only for an internal claim. — That is the rule of v0.22.0, and it holds.
- A file that contradicts a claim is the finding `grounding.file-contradicts-claim` at SHOULD. Its message names the path, and its evidence holds the quote of the claim and the quote of the file. — A SHOULD cannot make Build Ready harder to reach than today, and the author still sees the conflict.
- The profile setting `grounding.file_contradiction: MUST` raises that finding to MUST. — A team that trusts the result can make it block, and `grounding.contradicted-claim` stays a MUST by itself.
- The page "Change a profile" of the docs site shows that setting with its YAML, and the Check catalog has an entry for the new check. — The owner asked that a person can find how to raise the level.
- A linked spec doc can confirm a claim. It cannot contradict one in this stage. — The coherence stage owns a conflict with a linked doc at MUST, and a second finding for it is the noise of #119.
- An external link is not a source. — Its target needs a credential and a network fetch, and the author can save the text as a file of the bundle.
- The source policy does not apply to a file or a linked spec doc. — The policy judges a domain and a retrieval date, and a file has neither.
- Speccy keeps a result only when the quote is word for word in the file. With no such quote the claim is not settled by a file. — The coherence stage has the same rule, and a model can name text that it did not read.
- The main doc is not a source for its own claims. — A claim cannot confirm itself.
- Only text files count, up to the bound of 200,000 characters for assets, and bundle files come before linked spec docs. The run report names each file that Speccy left out. — The rubric stage has that bound, and a silent cut hides why a claim is unverified.
- The cache holds a file result by the claim and the content of the files, with no month. — A file changes only when its content changes, and a web page changes without notice.
- The answer of the model has an `analysis` key that sorts before the label. — The keys of an answer come in the order of the alphabet, and a label with no analysis in front was wrong in one answer of ten.
- The fix of an unverified claim says "Reference the doc that states this, or mark it as an assumption." — A file that the main doc references, in its folder or below it, is a carried file, which is the way to give evidence. A file above that folder is not carried.

## Out
- No scan of the root folder, and no source that the bundle does not hold.
- No fetch of an issue, a page, a repo path or a commit for a claim.
- No contradiction finding from a linked spec doc in the grounding stage.
- No change to the web search of the OpenRouter backend.
- No change to the level of `grounding.contradicted-claim` or `grounding.unverified-claim`.

## How I know it works
- A bundle with the main doc and a file `workflows.md` that states "team synapse owns the propagate operation": `speccy review <bundle> --stages grounding --format json` gives no finding for that claim, and the claim list of the run shows it verified with `workflows.md` as its source.
- The same bundle, with the file changed to "team atlas owns the propagate operation": the review gives one `grounding.file-contradicts-claim` at SHOULD, and the verdict is the same as before the change.
- The same bundle with `grounding.file_contradiction: MUST` in the profile: that finding is a MUST, and the verdict is Not Build Ready.
- A claim that only a linked spec doc states is verified with that doc as its source. A claim that a linked spec doc contradicts gives the coherence finding and no grounding finding.
- With a file that settles every claim, the run makes no call with web search.
- With a source policy that has an `allow` list of hosts, a file still confirms a claim.
- An edit to the file sends the claim to the model again. A second review with no edit sends nothing.
- The prompt, in turns with the prompt of v0.22.0 on the same bundle and a real model, gives no MUST that v0.22.0 does not give.
