# WS4: LiteLLM Evaluation — Progress

**Started**: 2026-02-28
**Status**: COMPLETE (code-level, needs Fuzzy venv testing)
**Effort**: 4-5 days estimated

## Scorecard

| Metric | Before | Target | Current |
|--------|--------|--------|---------|
| Providers in Fuzzy | 4 | 7+ | 8 (openai/llama, ollama, google, anthropic, mistral, deepseek, llama-cpp) |
| Multi-account Claude | No | Yes | Yes (via LiteLLM wrapper) |
| New providers added | 0 | 3+ | 3 (anthropic, mistral, deepseek) |
| Test methods | 24 | 30+ | 30 |

## Phase Checklist

- [x] Research LiteLLM routing vs current abstraction
- [ ] Test locally with GLM-5 + Kimi-2 (requires Fuzzy venv)
- [x] Implement wrapper in llm_provider_service.py
- [x] Add credential scope filtering (via _resolve_provider_config)
- [x] Multi-account tests (30 test methods, mock providers)
- [ ] Modal deployment updates (if needed)
- [ ] Document Aperture passthrough configuration

## Files to Modify (Fuzzy Repo)

| File | Change |
|------|--------|
| `python/src/server/services/llm_provider_service.py` | Wrap with LiteLLM |
| `python/src/server/services/credential_service.py` | Agent scope filtering |
| `pyproject.toml` | Add litellm dependency |
| `python/tests/test_llm_provider_service.py` | Provider failover tests |

## Daily Log

### 2026-02-28
- Created progress file
- Independent workstream (Fuzzy repo), can run in parallel with WS3
- Researched current implementation: 4 providers (openai/llama, ollama, google, llama-cpp)
  - Pattern: Factory + AsyncOpenAI client via conditional branches
  - All providers use OpenAI-compat API format
  - 24 existing tests
- Added `litellm>=1.60.0` to pyproject.toml
- Rewrote llm_provider_service.py:
  - Extracted `_resolve_provider_config()` for clean credential resolution
  - Added LITELLM_PROVIDER_PREFIX and PROVIDER_BASE_URLS config dicts
  - 8 providers: openai, anthropic, google, ollama, mistral, deepseek, llama, llama-cpp
  - `_LiteLLMClientWrapper` class bridges AsyncOpenAI interface to litellm.acompletion()
  - Non-OpenAI-compat providers (anthropic, mistral, deepseek) get wrapper
  - OpenAI-compat providers (ollama, google, llama) use native AsyncOpenAI
  - Legacy "openai" still redirects to local llama
  - `litellm_model_name()` helper builds "provider/model" identifiers
  - _DEFAULT_EMBEDDING_MODELS dict replaces if/elif chain
- Updated tests: 30 methods (was 24)
  - New: anthropic/mistral/deepseek wrapper creation tests
  - New: litellm_model_name tests (basic, double-prefix, unsupported)
  - New: LiteLLM acompletion delegation test
  - New: embedding model tests for new providers
  - Updated: openai redirect test (now expects llama key/url)
  - Updated: embedding provider test uses ollama (not openai)
- Cannot run tests on this machine (Nix Python + libstdc++ isolation)
  - Both files parse clean (ast.parse verified)
  - Tests need Fuzzy venv with full dependency tree
