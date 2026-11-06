---
name: vteam-submit
description: Submit the result of an interactive vteam session back to vteam, which commits, pushes, opens the PR or creates the tasks, and closes the session. Use when the user says they are done in a session vteam opened (its prompt has an "Interactive Session" section with a `vteam submit` command).
disable-model-invocation: true
---

The user is done with this interactive vteam session. Submit the result now.

1. Find the `## Interactive Session` section of the first prompt in this conversation. It has the exact `vteam submit ...` command and the JSON shape to submit. If there is no such section, tell the user this is not a vteam session and stop.
2. Write the JSON object for everything done in this session, following that shape:
   - `status` and `summary` are required. `summary` is what was done, in a sentence or two.
   - Include `commitMessage` (and `filesChanged`) when files were changed and not yet committed. Do not run `git commit` or `git push` yourself; vteam commits uncommitted changes with your message and pushes any commits the user made.
   - For an agent that reports findings, put every finding the user agreed to in `content` as `{ "type": "tasks", "body": [ ... ] }`.
   - Add `threadReplies` or `memoryUpdate` only when the prompt's shape offers them.
3. Run the command with the JSON on stdin, exactly like this:

   ```
   <the vteam submit command from the prompt> <<'VTEAM_OUTPUT'
   { ... }
   VTEAM_OUTPUT
   ```

4. If it fails, read the error. Fix invalid JSON and run it again. For anything else (a rejected push, a closed PR), tell the user what failed; the session stays open, so they can sort it out and you can submit again.
5. When it succeeds, tell the user what vteam did (PR, tasks created). Shortly after, vteam removes the worktree and closes this window (a window in vteam's own tmux session, used when vteam ran outside tmux, stays open for the user to close).
