# One owner of the local state

## What it does
In local mode, one process owns a state folder. It holds the store, the review worker, the progress of each run and the watch on the files. Each other Speccy process in the same folder is a client: it sends its API calls to the owner and opens no store. A review that the app queues runs in the owner, and the app shows its stages live, whichever process is the owner. When the owner stops, a client takes over, and a review of the old owner ends at once with a message. Issue #87.

## Decisions
- The first process to start in a folder takes the owner lock in `.speccy/state/`. A later process is a client. — One process then holds the queue, the progress and the file watch, so no process takes a job of another.
- The lock is a file lock that the operating system frees when the process ends, as `migrate.lock` is. — A hard kill must not leave a lock that a person has to remove.
- Every local process can be the owner: the app, `speccy mcp`, `speccy tui` and a CLI command with a state folder. — One rule needs no special case for each command.
- The owner opens a listener for processes on 127.0.0.1 with a random port. The lock file holds the address, a random token, the Speccy version, the kind of process and the process ID. — A client needs all of them, and reads them with no call.
- The process listener accepts a call only with the token. — A web page in the browser can reach a loopback port, and it cannot read a file in the state folder.
- A client sends each API call to the owner. `speccy mcp` and `speccy tui` use their API client for this. The app serves its own pages and passes the browser's API calls on. — The API is already the one way in for each of them.
- The owner always watches the folder, also when it is a CLI command. — A client app must see an edit on disk while another process is the owner.
- An owner that wants to exit first finishes each job in its queue, including a job that a client queued. — A review that the app queued must not fail because a CLI command ended.
- When the owner stops, a client takes the lock on its next call, opens the store, and is the owner. — The work of the person goes on with no restart.
- A new owner ends each run that it finds queued or running, with the message that the process that ran the review stopped. — With one owner, such a run has no process behind it, so the doc can be reviewed again at once.
- A local owner starts one run of a doc at a time, under one lock in the process. — Two starts at the same instant must not make two runs.
- A client whose version differs from the owner's stops with a message and changes nothing. The message names both versions, the kind of the owner and its process ID. — A call that the owner's version does not have must not half work.
- A CLI command with a temporary state, or with `SPECCY_STATE_DIR`, owns that state folder. — It shares the folder with no other process.
- Hosted mode does not change. — It has one replica and one worker on Postgres.

## Out
- No owner column in the job table, and no progress events in the store. One owner makes both needless.
- No Origin or Host check on the browser port of the local app. That gap exists today and gets its own issue.
- No `/mcp` over HTTP in local mode (#88), and no root flag for `speccy mcp` (#89).
- No move of a running review from an owner that stops to the new owner.
- No second hosted replica.

## How I know it works
- Start `speccy mcp` in a folder, then the app. Run a review from the app. The app shows each stage live, and `.speccy/state/` has one lock file that names `speccy mcp`.
- With the app as the owner, call `review_bundle` over `speccy mcp`. The app shows the run and its stages, and the store has one job.
- Kill the owner with `kill -9` during a review. The next call of the client works. The run shows the message that its process stopped, and **Run review** starts a new run at once.
- Run `speccy review` with a state folder, and queue a review in the app while it runs. The command exits after both reviews end, and both have a verdict.
- Call the process listener with no token, with `curl`. The answer is 401.
- Start a client of another Speccy version. It prints both versions, the kind and the process ID of the owner, and exits with no change to the store.
- `lsof` on `speccy.db` shows one process, with two or more Speccy processes in the folder.
