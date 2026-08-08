---
kind: task
id: integration-test
agent: integration-test
---

Run the `integration-test` agent against one `e2e/tests/` suite, using past Action logs
as reference. Fixes flakiness or adds edge-case tests, and persists findings to the running automation via the automation service.
