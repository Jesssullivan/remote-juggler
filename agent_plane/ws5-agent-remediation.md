# WS5: Agent Remediation Gap — Progress

**Started**: 2026-02-28
**Status**: COMPLETE
**Effort**: 5-6 days estimated, ~0.5 day actual

## Scorecard

| Metric | Before | Target | Current |
|--------|--------|--------|---------|
| Issues filed by agents | 36+ | N/A | 36+ |
| PRs created by agents | 0 | 1+ | Enabled (pending live run) |
| Campaigns with createPRs | 0 | 3+ | 3 |
| Runner tests passing | 77 | Yes | 86 (77 + 9 new PR tests) |

## Phase Checklist

- [x] Schema extension (campaign.go CampaignOutputs + feedback.go Finding struct)
  - `CampaignOutputs`: Added `PRBranchPrefix`, `PRBodyTemplate`
  - `Finding`: Added `Fixable`, `RemediationType`, `RemediationHints`
- [x] FeedbackHandler PR logic (ProcessPRFindings, createBranch, patchFile, createPullRequest)
  - Full GitHub API flow: branch creation → file fetch/patch → PR creation
  - Dedup via `prExists()` (checks for open PR with same head branch)
  - `prBranchName()`: deterministic from prefix + fingerprint
  - `buildPRBody()`: default template + custom `PRBodyTemplate` support
- [x] Scheduler integration (conditional PR processing)
  - Added `ProcessPRFindings` call in `storeResult()` after `ProcessFindings`
  - Guarded by `campaign.Feedback.CreatePRs && !campaign.Guardrails.ReadOnly`
- [x] Unit tests (9 new tests, all passing)
  - TestPRCreationE2E: Full branch → patch → PR flow
  - TestPRSkipsNonFixableFindings: Guards on Fixable + RemediationHints
  - TestPRSkipsReadOnlyCampaign: Guards on ReadOnly
  - TestPRSkipsWhenCreatePRsDisabled: Guards on CreatePRs
  - TestPRDeduplicatesExistingPR: Skips when PR already exists
  - TestPRBranchNaming: Deterministic branch names
  - TestPRBodyGeneration: Default template
  - TestPRBodyTemplate: Custom template with placeholders
  - TestSchedulerPRFeedbackIntegration: Full scheduler→feedback→PR path
- [x] Campaign migration (3 HexStrike campaigns enabled)
  - `hs-dep-vuln.json`: createPRs=true, readOnly=false, prBranchPrefix="sid/dep-update-"
  - `hs-container-vuln.json`: createPRs=true, readOnly=false, prBranchPrefix="sid/container-fix-"
  - `hs-gateway-pentest.json`: createPRs=true, readOnly=false, prBranchPrefix="sid/security-fix-"
- [x] Schema.json updated with `prBodyTemplate` field

## Files Modified

| File | Change |
|------|--------|
| `test/campaigns/runner/campaign.go` | +2 fields on CampaignOutputs |
| `test/campaigns/runner/feedback.go` | +3 fields on Finding, +7 methods (ProcessPRFindings, prBranchName, prExists, createBranch, patchFile, createPullRequest, buildPRBody) |
| `test/campaigns/runner/feedback_test.go` | +9 tests, expanded mock to handle git refs/contents/pulls |
| `test/campaigns/runner/scheduler.go` | +3 lines in storeResult() |
| `test/campaigns/schema.json` | +prBodyTemplate field |
| `test/campaigns/hexstrike/hs-dep-vuln.json` | createPRs=true, readOnly=false, +prBranchPrefix |
| `test/campaigns/hexstrike/hs-container-vuln.json` | createPRs=true, readOnly=false, +prBranchPrefix |
| `test/campaigns/hexstrike/hs-gateway-pentest.json` | createPRs=true, readOnly=false, +prBranchPrefix |

## Verification

```bash
# All runner tests pass (86 total)
cd test/campaigns/runner && go test ./...

# PR-specific tests
cd test/campaigns/runner && go test -v -run "TestPR|TestSchedulerPRFeedback" ./...
```

## Daily Log

### 2026-02-28
- Created progress file
- Schema extension: Added PR fields to CampaignOutputs and Finding
- FeedbackHandler: Implemented full PR creation flow (branch → patch → PR) with dedup
- Scheduler: Added PR feedback call in storeResult()
- Tests: 9 new tests covering E2E, guards, dedup, naming, body generation
- Campaign migration: 3 HexStrike campaigns enabled for PR creation
- WS5 COMPLETE — 86 tests passing, 3 campaigns enabled
