# Architecture decisions

| # | Decision | In one line |
| --- | --- | --- |
| [0001](0001-use-hooks-and-a-shared-sqlite-inbox-for-cross-harness-delive.md) | Use hooks and a shared SQLite inbox for cross-harness delivery | Use hooks and a shared SQLite inbox for portable cross-harness delivery. |
| [0002](0002-bind-delivery-identities-to-resumable-harness-sessions.md) | Bind delivery identities to resumable harness sessions | Treat each resumable harness session as a distinct agent and bind hooks and MCP calls to it. |
| [0003](0003-enforce-mandatory-channels-as-delivery-policy.md) | Enforce mandatory channels as delivery policy | Mirror human-configured mandatory channels into SQLite and apply them during recipient resolution. |
