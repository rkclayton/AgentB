# AgentB comparative task suite

These ten Windows-native tasks are reduced from AgentB defects whose repairs have already
shipped. Each trial starts from the byte-identical `fixture/seed` tree. A brief names the symptom
and target, never the repair. `manifest.json` binds each brief to one targeted verifier and one
known-good oracle diff.

The suite shape follows Terminal-Bench's instruction/environment/verifier/oracle separation.
The tasks themselves are AgentB work, not borrowed benchmark tasks. Run the model-free proof with:

`node tests/comparative/verify-fixtures.mjs`
