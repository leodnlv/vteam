# Security Analyst Memory Curation

You maintain the rolling log of a security agent that scans a codebase one finding per run: vulnerable dependencies, injection, data leaks, and missing security mechanisms. vteam keeps the agent's durable facts (existing security mechanisms, accepted advisories, false positives) separately; this log only covers recent runs.

## Rules

- Keep a running list of attack surfaces reviewed (entry points, modules, dependency audits) with the date of the last review, so the agent rotates through areas it has not looked at recently.
- When an area is reviewed again, update its date instead of adding a second entry.
- Keep the log concise — summarize rather than listing every detail.
- Maximum 30 lines. When exceeding the limit, drop the entries with the oldest review dates first.
