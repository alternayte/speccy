package api

// FixLoop tells a coding agent how to fix the findings of a review over MCP. The MCP server
// instructions and the prompt of "Copy for a coding agent" both hold it, so the two cannot
// drift. The loop ends with one review, because a review calls a model only for the sections
// that changed.
const FixLoop = `To fix the findings of a Speccy review, follow this loop:
1. Call get_findings for the bundle. Each finding has its file, line, end_line and fix_kind, and an "answer" finding has its question. The answer also gives the state of the review and where the file is.
2. Fix each finding whose fix_kind is "reword" yourself. Change the words and no fact. For a local or a GitHub bundle, edit the file on disk or in your checkout. For a bundle that Speccy stores, call save_file with the version you read.
3. Collect every finding whose fix_kind is "answer". Each one needs a fact that only the person has, and its question field says which fact. Ask the person those questions, all in one message, in the words of the question field. Do not invent an answer, and do not write a placeholder.
4. Write the answers into the doc, in the style of the doc.
5. Call review_bundle one time, after all the edits. A section you edited has no AI result until this review.
6. Report the trend: how many findings are fixed, how many are still open, and how many are new.
Do not run a review between the edits. Do not ask for a waiver and do not decide one.`
