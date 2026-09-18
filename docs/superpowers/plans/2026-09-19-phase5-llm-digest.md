# Healarr Phase 5 — LLM digest Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One Claude Haiku 4.5 call per day that writes the digest narrative (reconciling both nodes' findings and explaining staleness picks), with a per-day USD budget, a cost log in `llm_calls`, a `--no-llm` switch, and the existing templated digest as the always-available fallback — which stays the default on hosts without an API key.

**Architecture:** `internal/llm` wraps `github.com/anthropics/anthropic-sdk-go` behind a tiny `Client` interface (`Narrate(ctx, Prompt) (Narrative, Usage, error)`) with a fake transport for tests; a pure `BuildPrompt(DigestInput) Prompt` renders the deterministic facts (findings, staleness, cleanup plans, peer state) into a bounded prompt (token-capped, no secrets); `Budget` reads today's spend from `llm_calls` and refuses calls over `llm.daily_budget_usd`; `agent.SendDigest` calls `llm.Narrate` only when `cfg.LLM.Enabled && secrets.anthropic_api_key != "" && !noLLM` and prepends the narrative to the templated body, falling back silently-but-logged on any error. `healarr report generate --no-llm` and `healarr digest preview [--no-llm]` exercise both paths.

**Tech Stack:** Go 1.25, `github.com/anthropics/anthropic-sdk-go` (pin the current release), model `claude-haiku-4-5-20251001`, existing store/notify/agent packages.

**Spec:** `docs/superpowers/specs/2026-09-12-go-rewrite-design.md` — C1 (LLM lib/model), C3 (`llm_calls`), C4 (daily digest → LLM narrative), C8 phase 5.

## Global Constraints
- Module `github.com/parsoFish/healarr`, `go 1.25.0`; `CGO_ENABLED=0` cross-builds for arm64/amd64.
- Files < 400 lines; errors wrapped; nothing swallowed (an LLM failure is logged, recorded in `llm_calls` with `ok=0`, and the templated digest is sent regardless).
- No secrets in prompts or logs: the prompt contains findings text only; API key never logged; `llm_calls` stores tokens/cost/model/error text only.
- **Default off in practice:** `llm.enabled = false` stays in the deployed configs until the user adds `anthropic_api_key`; with an empty key the code path must not even construct the SDK client.
- Budget: `daily_budget_usd` (default 1.0) enforced from `llm_calls` sums for the local calendar day; pricing constants for Haiku 4.5 live in `internal/llm/pricing.go` (config-overridable `llm.input_usd_per_mtok`, `llm.output_usd_per_mtok`).
- Prompt size capped (`llm.max_prompt_chars`, default 24000) — truncate the lowest-severity findings first, never the summary line.
- Tests ≥ 80 %: SDK calls through an injected `http.RoundTripper` fake returning canned Messages API responses; budget tests against the temp DB.
- No AI attribution trailers; lint clean.

---
### Task 1: Config — `[llm]` additions + secrets wiring
Add `LLM.{MaxPromptChars int, InputUSDPerMTok, OutputUSDPerMTok float64, Timeout time.Duration}` with defaults (24000, Haiku 4.5 list prices, 60 s), validation (> 0), example TOML; `config validate` prints `llm_enabled` and `anthropic_api_key: set|missing`. Tests. Commit `feat(config): llm budget, pricing and prompt limits`.

### Task 2: Store — `llm_calls`
`RecordLLMCall(ctx, LLMCall{CalledAt, Model, InputTokens, OutputTokens, CostUSD, OK bool, Error string}) (int64, error)`, `LLMSpendSince(ctx, since time.Time) (float64, error)`, `RecentLLMCalls(ctx, limit) ([]LLMCall, error)` (table exists in 0001). Tests. Commit `feat(store): llm call log and daily spend`.

### Task 3: `internal/llm` — prompt builder, client, budget, fallback
`Prompt{System, User string}`, `BuildPrompt(in notify.DigestInput, max int) Prompt` (pure; deterministic ordering; truncation rule; includes both nodes, staleness top-N, cleanup plans, peer state); `Narrative{Text string}`, `Usage{InputTokens, OutputTokens int}`; `Client` interface + `Anthropic` impl via the SDK with `WithHTTPClient` injection + `Timeout`; `Cost(usage, pricing) float64`; `Budget{Store, Limit}.Allow(ctx, now) (bool, spent float64, error)`; `Narrate(ctx, Deps, in) (string, Usage, error)` orchestrating budget → client → record, returning `ErrBudgetExceeded`/`ErrDisabled` sentinels. Fake transport tests: success, 429/5xx error recorded with ok=0, budget refusal (no HTTP call), timeout, prompt truncation. Commit `feat(llm): haiku digest narrative with budget, cost log and fake transport`.

### Task 4: Agent + notify + CLI wiring
`agent.Options.LLM llm.Client` (nil when disabled or no key); `SendDigest` → narrative prepended under a `NARRATIVE` heading (template gains `{{if .Narrative}}`), fallback on error with one Warn log; `agent serve` wires it; `healarr report generate --no-llm`, `healarr digest preview [--no-llm] [--json]` (renders exactly what would be emailed, without sending); `notify test` unchanged. Tests through fakes. Commit `feat(agent,cli): llm narrative in the daily digest with --no-llm fallback`.

### Task 5: Host deployment (operator) — build; deploy binaries; configs keep `llm.enabled = false` until the user adds a key; verify `digest preview --no-llm` on the Pi; probe.

### Task 6: Docs — README (Phase 5 ✅), runbook (enabling the LLM: key, budget, what the log shows, forcing fallback), architecture, ADR-020 (single daily Haiku call, budget gate, templated fallback as the contract).

## Self-review notes
Spec C8 phase 5 rows: Haiku wrapper ✓ T3; budget/cost log ✓ T2/T3; templated fallback ✓ T4; `--no-llm` ✓ T4. Deployed default stays fallback-only (brief requirement). Type flow: `notify.DigestInput` → `llm.BuildPrompt` → `Narrative` → `DigestInput.Narrative` → template.
