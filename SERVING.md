# Serving verification

Verified on 2026-09-02 against HOMEPC over a Tailscale-protected SSH session. The serving endpoint remains loopback-only on HOMEPC; remote administration and probes travel through SSH.

## Host discovery

- OS: Microsoft Windows 11 Pro 10.0.26200, PowerShell 5.1 remotely.
- GPU: NVIDIA GeForce RTX 5070 Ti, 16,303 MiB, driver 591.86, driver-reported CUDA 13.1. The GPU also drives the display.
- RAM: 31.91 GiB. Model storage is on `E:` because `C:` had only 11.69 GiB free; `E:` had about 471 GiB free before downloads.
- Ollama 0.23.2 was the prior stack and listens on port 11434. Its configured store is `D:\AI\models`; both the API model list and that store were empty during discovery. No effective prior `num_ctx` could be recovered.
- Ports 8080, 1234, 8000, and 5000 were unused. No LM Studio, llama.cpp, ComfyUI, or Python model process was active.

## llama.cpp installation

Installed the official Windows CUDA 13.3 assets from llama.cpp release `b10775` to `E:\llama`. The server reports build `10775`, commit `67a17c17c`, and detects `CUDA0: NVIDIA GeForce RTX 5070 Ti (16302 MiB, 15011 MiB free)`.

The packaged CUDA runtime operates correctly with the installed driver despite `nvidia-smi` reporting CUDA 13.1. No command-line flag was rejected. The server is bound to `127.0.0.1`, so its no-key/CORS warning does not expose it to the LAN or tailnet.

## Model and ladder

Downloaded `unsloth/Qwen3.8-27B-GGUF/Qwen3.8-27B-UD-Q3_K_XL.gguf` (13,146,393,504 bytes) to `E:\Models\Qwen3.8-27B`. Vision projectors were not downloaded. The available MTP draft is `MTP/mtp-Qwen3.8-27B-Q4_0.gguf` (1,369,590,656 bytes).

| Attempt | Result | VRAM used/free after load |
|---|---|---:|
| IQ4_XS / 32K | Skipped: the display uses this GPU | — |
| Q3_K_XL / 40,960 / q8_0 | Healthy, but failed the 400 MiB margin | 15,768 / 230 MiB |
| Q3_K_XL / 40,960 / q8_0, token embedding on CPU | Healthy, still below margin; startup also rose from about 21 s to 109 s | 15,788 / 210 MiB |
| Q3_K_XL / 32,768 / q8_0 | Pass; winning rung | 14,504 / 1,494 MiB |

All required model layers are GPU-offloaded in the winning configuration. `--fit off` prevents automatic reductions, and the reported slot context is exactly 32,768.

## Canary results

1. **Health and properties:** `/health` returned 200. `/props` reported build `b10775-67a17c17c`, `n_ctx=32768`, the pinned model path, supported tool/reasoning template capabilities, and `modalities.vision=false`.
2. **Overflow:** a 34,825-token request returned HTTP 400: `request (34825 tokens) exceeds the available context size (32768 tokens), try increasing it`. No truncation occurred.
3. **Tokenizer:** `/tokenize` returned 1,000 tokens for the fixed 1,000-character `0123456789` sample.
4. **Tool calling:** the required call parsed natively as `read_file` with JSON arguments `{"path":"main.go"}`. Appending the assistant call and matching tool result produced a normal text response with finish reason `stop`. `/slots` reported `peg-native`.
5. **Reasoning:** per-request `chat_template_kwargs` works. The tool prompt produced 12 reasoning tokens at low, 10 at medium, 15 at xhigh, and zero with thinking disabled.
6. **Prefix cache:** prompt/cached tokens were 3,260/0, 3,339/3,323, and 3,416/3,402. Final reuse ratio was 0.9959 without extra flags.
7. **Streaming:** the final chunk contained both usage and timings; progress chunks were present; tool-call arguments arrived incrementally in six deltas. A raw transcript is stored under `serve/probes/samples/`.
8. **Speed and MTP:** baseline was 1,730.34 prompt tok/s and 50.36 generated tok/s on a 4,020-token input. MTP reached 1,450.52 prompt tok/s and 88.18 generated tok/s and preserved tool calls, but left only 205 MiB VRAM free. MTP remains off for the required safety margin.
9. **Sampling:** `/props` confirmed temperature 0.6, top-k 20, top-p 0.95, min-p 0, repeat penalty 1.0, and mirostat 0.
10. **Prior stack:** Ollama is installed, but its API and configured model store were empty. The earlier experience cannot be attributed to a recoverable current `num_ctx`; a small historical context remains plausible but unverified.
11. **Accounting endpoints:** on the final clean rerun, median `/tokenize` time was 8 ms idle and 8 ms during generation. `/apply-template` was 11 ms idle and 12 ms during generation. Neither blocks on the generation slot. The server log contained neither `forcing full prompt re-processing` nor `failed to truncate`.

## Reliability

C1 was the selected Qwen3.8-27B `UD-Q3_K_XL` configuration at 32,768 context. C2 was `UD-IQ4_XS` at 16,384 context; it loaded with all 99 requested GPU layers and `--fit off`, so the quality comparison did not need partial offload.

### C1 — UD-Q3_K_XL / 32K

| trial | task | completed | wrong_tool | invalid_args | turns | pass |
|---|---|---:|---:|---:|---:|---:|
| add-1 | add | true | 0 | 0 | 4 | true |
| add-2 | add | true | 0 | 0 | 5 | true |
| add-3 | add | true | 0 | 0 | 4 | true |
| fix-1 | fix | true | 1 | 1 | 6 | true |
| fix-2 | fix | true | 0 | 0 | 9 | true |
| fix-3 | fix | true | 0 | 0 | 6 | true |

Passes: 6/6.

### C2 — UD-IQ4_XS / 16K

| trial | task | completed | wrong_tool | invalid_args | turns | pass |
|---|---|---:|---:|---:|---:|---:|
| add-1 | add | true | 0 | 0 | 4 | true |
| add-2 | add | true | 0 | 0 | 4 | true |
| add-3 | add | true | 0 | 0 | 4 | true |
| fix-1 | fix | true | 0 | 0 | 6 | true |
| fix-2 | fix | true | 0 | 0 | 6 | true |
| fix-3 | fix | true | 0 | 0 | 6 | true |

Passes: 6/6.

Both configurations cleared the 4/6 bar and tied, so the fixed verdict rule selects C1. The higher quant was not at least two passes better; retaining C1 preserves 32K context and its measured display-GPU margin. No C3 test was triggered.

After the verdict, C1 was restarted and prompt-1 canaries 1, 2, and 8 were rerun: health and props returned HTTP 200 with `n_ctx=32768`; the 34,825-token overflow request returned HTTP 400 without truncation; prompt processing measured 1,729.75 tok/s and generation measured 50.27 tok/s.

## Facts

os=windows
shell=powershell
codex_in_wsl=no
gpu=NVIDIA GeForce RTX 5070 Ti
driver=591.86
llama_build=b10775
llama_path=E:\llama\llama-server.exe
base_url=http://127.0.0.1:8080
model_alias=qwen3.8-27b
model_file=E:\Models\Qwen3.8-27B\Qwen3.8-27B-UD-Q3_K_XL.gguf
quant=UD-Q3_K_XL
provisional=no
n_ctx=32768
kv_type=q8_0
mtp=off
vram_free_after_load_mb=1608
vram_peak_used_mb=14390
overflow_behavior=error
overflow_error_substring=exceeds the available context size
tool_calls_parsed_by_server=yes
chat_format=peg-native
reasoning_control=chat_template_kwargs
reasoning_tokens_medium=10
cache_reuse_ratio=1.00
cache_reuse_requires=none
stream_final_chunk_has_usage=yes
stream_final_chunk_has_timings=yes
return_progress=yes
tool_call_deltas=incremental
prompt_tps=1725
gen_tps=50
reliability_c1=6/6
reliability_c2=6/6
reliability_verdict=C1
reliability_note=tied; C1 retained for 32K context and GPU margin
tokenize_idle_ms=8
tokenize_busy_ms=8
apply_template_idle_ms=11
apply_template_busy_ms=12
tokenize_blocks_on_slot=no
prior_stack=ollama
prior_stack_ctx=unknown
