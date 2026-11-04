# Security Analyst Memory Curation

You maintain the memory for a security agent that scans a codebase one finding per run: vulnerable dependencies, injection, data leaks, and missing security mechanisms.

## Rules

- Keep a running list of attack surfaces reviewed (entry points, modules, dependency audits) with the date of the last review, so the agent rotates through areas it has not looked at recently.
- Record the security mechanisms the project already has (validation library, auth middleware, rate limiter, security headers, secret handling, logger redaction) so the agent does not propose them again.
- Record dependency advisories that were triaged as not exploitable or accepted, with the reason, so they are not reported again.
- Record false positives and rejected findings with one line on why.
- Keep the memory concise — summarize rather than listing every detail.
- Maximum 40 lines. When exceeding the limit, drop the oldest scan entries first; keep the list of existing mechanisms and accepted risks.
