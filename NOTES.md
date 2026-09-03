# Implementation notes

## Prompt 1 assumptions and deviations

- HOMEPC is the target serving machine. Work was performed remotely over Tailscale and Windows OpenSSH because the repository workspace is on another Windows machine.
- `E:\llama` and `E:\Models` replace the documented `C:` defaults because `C:` had insufficient room for the 13.1 GB model while `E:` had about 471 GB free.
- llama.cpp `b10775` was the newest official build release with Windows CUDA artifacts at installation time. The CUDA 13.3 bundle was selected because Blackwell requires CUDA 12.8 or newer; direct device discovery and inference verified compatibility with driver 591.86.
- The exact model download was `hf download unsloth/Qwen3.8-27B-GGUF Qwen3.8-27B-UD-Q3_K_XL.gguf --local-dir E:\Models\Qwen3.8-27B`. The MTP draft was downloaded separately only after baseline passed.
- Rung 1 was skipped because the RTX 5070 Ti drives the display. The required near-margin retry of rung 2 used `-ot token_embd.weight=CPU`; it did not restore the margin and is off by default.
- MTP improved decode by more than 20% and retained valid tool calls, but its 205 MiB remaining VRAM violated the safety margin. The files and switch remain available for later testing; the default is off.
- No prescribed flag was rejected. No context-shift, cache-reuse, chat-template default, reasoning-budget, or checkpoint flag was added.
- Ollama 0.23.2 was running, but `/api/tags`, `/api/ps`, `D:\AI\models\manifests`, and `D:\AI\models\blobs` were empty. Therefore its prior effective context is recorded as unknown.
- Anonymous Pollinations endpoints returned 401/402 and were rejected as a public-provider candidate. Ollama Cloud was reachable but returned 401 until the desktop app is signed in; no credential was stored.
- The shell account and SSH keys are operational access only. They are excluded by `.gitignore` and never appear in tracked files.

## Operational cautions

- `llama-server` intentionally has no API key because it binds only to `127.0.0.1`. Use SSH forwarding for remote API access; do not expose port 8080 directly to the LAN or tailnet without authentication.
- The shell tool built later is not a security boundary. The workspace must remain disposable, commands are logged, and OS-level isolation is deferred as specified in `INTERFACES.md`.
