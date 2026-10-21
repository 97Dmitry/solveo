# Parallel answers implementation plan

> **For agentic workers:** Use superpowers:executing-plans. User requested implementation in a separate branch; do not wait for another approval, commit or push.

**Goal:** Compare one Codex answer with parallel concise approach and full solution.

**Architecture:** An opt-in chat configuration launches two independent Generate calls under one controller operation. The main request owns continuation state; the approach starts a separate session with bounded textual history. Two answer blocks belong to the user turn and persist independently. One worker owns all archive writes; child requests return immutable results through a buffered channel.

**Tech Stack:** Existing Go, Wails, React, strict TypeScript/Zod, Vite+, pnpm; no new dependencies.

**Spec:** User-approved scenario in chat: same screenshot, independent fast/main models and efforts, approach before solution if available, cancel both, retry main without repeating successful approach. The former classifier roadmap is superseded for this experiment.

## Global constraints

- Current checkout, branch feat/parallel-answers; no commits/pushes.
- Preserve exec --ignore-user-config, sandbox, exact main session, process-tree cancellation.
- Persist input/images before either request; archive results before cleanup. Save failures cancel/drain both requests and retain all results for explicit RetrySave.
- Existing archives without new fields remain readable; interrupted blocks never auto-retry.
- Two blocks may disagree. Prompts must allow correcting errors; no claimed consistency or guaranteed latency.
- No real provider calls in tests. User measures Windows runtime; show per-block elapsed time.

## Review focus

- Main finishes first: cancel redundant approach and prevent a late result from appearing.
- Approach survives main error, explicit main-only retry, config edits and restart.
- Save failure while main runs: retain any completed main state/result; no concurrent archive writes.
- New input during requests remains intact; quick capture sources survive failures.
- Missing/obsolete models and efforts cannot silently select another advertised value.

### Task 1: Archive and request contracts

Files: internal/chat/chat.go, internal/history/{validation,store}.go, internal/llm/provider.go, internal/llm/codex/provider.go and tests.
Interfaces: Config adds parallel_answers, fast_model, fast_reasoning; Message adds optional answers and request_config. Answer includes kind, status, text, error, attempt_id, model, reasoning, elapsed_ms. Request adds purpose and context text.
- [x] Add failing archive roundtrip/interruption and purpose-specific prompt tests; run targeted suites.
- [x] Add contracts, validate blocks and links, normalize interrupted requests, prompt fast/main separately. Existing single-request prompt remains unchanged.
- [x] Run archive and adapter tests.

### Task 2: Parallel controller

Files: internal/app/operation.go, parallel.go, controller.go and parallel_test.go.
Interfaces: existing Submit/Cancel/RetrySave; same Snapshot message list with nested answers. Request config is frozen on accepted turn and reused for retry.
- [x] Add gated provider tests showing both calls start before completion, approach appears/saves early, isolated state, main-only retry, cancellation, main-first, save failure and restart recovery.
- [x] Implement per-operation result coordinator, ordered blocks, independent state, immutable inputs, bounded context. Main result governs user turn status and cleanup.
- [x] Run controller tests with race detector.

### Task 3: Settings and answer UI

Files: frontend/src/{bridge,state/chat/settings/app} as applicable; generated frontend/wailsjs via make bindings.
Interfaces: schema checks generated Go keys, optional archive fields normalized in Zod; model effort selectors independent.
- [x] Add failing settings and chat tests for independent models, block statuses, main-only retry and copy code.
- [x] Add opt-in configuration, answer blocks with times/errors, correct scrolling and last-code selection. No fake text streaming.
- [x] Run bindings, frontend checks/tests/build.

### Task 4: Verification and documentation

- [x] Update roadmap stage 1, product instructions, acceptance steps and module AGENTS/CLAUDE imports.
- [x] Run make check, required Go race suites and diff check (exclude Wails-generated whitespace; do not hand-edit generated bindings).
- [x] Fresh review whole uncommitted branch; address substantive findings and verify regressions. Report real Windows runtime/latency as unverified. Leave changes uncommitted.
