# Guarantees

This table lists only the guarantees whose tests pass today.

| Guarantee                                                                                       | Test                                                                                  |
| ----------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| An open MUST finding gives Not Build Ready.                                                     | [`TestVerdict_OpenMustBlocks`](internal/engine/verdict/verdict_test.go)               |
| A valid waiver on the only MUST finding gives Build Ready.                                      | [`TestVerdict_WaivedMustPasses`](internal/engine/verdict/verdict_test.go)             |
| An open blocking thread gives Not Build Ready.                                                  | [`TestVerdict_BlockingThreadBlocks`](internal/engine/verdict/verdict_test.go)         |
| A verdict for an old version reads as stale.                                                    | [`TestVerdict_OldVersionIsStale`](internal/engine/verdict/verdict_test.go)            |
| A waiver becomes invalid when its section changes.                                              | [`TestWaiver_InvalidatedOnSectionEdit`](internal/app/collab_test.go)                  |
| The waiver policy is enforced for each policy value.                                            | [`TestWaiverPolicy_Table`](internal/features/waiver/waiver_test.go)                   |
| The author cannot approve their own bundle.                                                     | [`TestApproval_AuthorCannotApprove`](internal/app/collab_test.go)                     |
| A content change revokes approvals.                                                             | [`TestApproval_EditRevokes`](internal/app/collab_test.go)                             |
| SHOULD findings never change the verdict.                                                       | [`TestVerdict_ShouldNeverBlocks`](internal/engine/verdict/verdict_test.go)            |
| The verdict function is pure: same input, same output.                                          | [`TestVerdict_Deterministic`](internal/engine/verdict/verdict_test.go)                |
| Lint finishes a 10,000-word doc in under 1 second.                                              | [`BenchmarkLint_10kWords`](internal/engine/lint/lint_test.go)                         |
| A mapped file with no frontmatter is reviewed with the mapped profile; frontmatter `type` wins. | [`TestConfig_PathMapping`](internal/features/review/review_test.go)                   |
| A relaxed check reports as INFO and never blocks; removing it restores its level.               | [`TestAdoption_RelaxedCheck`](internal/features/review/review_test.go)                |
| Both store engines pass the same conformance suite.                                             | [`TestStoreConformance`](internal/store/conformance/conformance_test.go)              |
| A concurrent append with a stale version is rejected.                                           | [`TestEventStore_ConcurrentAppendRejected`](internal/es/es_test.go)                   |
| Projections update in the same transaction as the append.                                       | [`TestEventStore_InlineProjectionAtomic`](internal/es/es_test.go)                     |
| Invalid model JSON is retried once, then the step fails.                                        | [`TestModel_InvalidJSONRetryOnce`](internal/model/model_test.go)                      |
| Injected instructions in a doc do not change the verdict.                                       | [`TestInjection_DocCannotChangeVerdict`](internal/features/review/pipeline_test.go)   |
| Injected instructions in an MCP result do not change the verdict.                               | [`TestInjection_MCPResultIsData`](internal/features/review/pipeline_test.go)          |
| An unverified claim is a SHOULD finding; a contradicted claim is MUST.                          | [`TestGrounding_Labels`](internal/features/review/pipeline_test.go)                   |
| An unchanged section is not sent to a model again.                                              | [`TestCache_UnchangedSectionReused`](internal/features/review/pipeline_test.go)       |
| Every run records the profile version it used.                                                  | [`TestRun_PinsProfileVersion`](internal/features/review/pipeline_test.go)             |
| A split in reader answers creates a divergence finding.                                         | [`TestDivergence_SplitIsFinding`](internal/features/review/divergence_test.go)        |
| All `NOT SPECIFIED` on a MUST question creates a MUST gap finding.                              | [`TestDivergence_GapOnMust`](internal/features/review/divergence_test.go)             |
| An answer with an invented quote is treated as `NOT SPECIFIED`.                                 | [`TestDivergence_InventedQuoteRejected`](internal/features/review/divergence_test.go) |
| One model for all readers gives "low reader diversity" and does not block.                      | [`TestDivergence_LowDiversityFlagged`](internal/features/review/divergence_test.go)   |
| Readers never receive other readers' answers or the rubric.                                     | [`TestDivergence_ReaderIsolation`](internal/features/review/divergence_test.go)       |
| An uncovered upstream REQ is a MUST finding.                                                    | [`TestCoherence_UncoveredReqIsMust`](internal/features/review/coherence_test.go)      |
| A standalone acknowledgement makes coherence not applicable.                                    | [`TestCoherence_StandaloneAck`](internal/features/review/coherence_test.go)           |
| An upstream edit marks downstream verdicts stale.                                               | [`TestCoherence_UpstreamEditStales`](internal/features/review/coherence_test.go)      |
| Restatement above the threshold is a SHOULD finding.                                            | [`TestCoherence_RestatementShingles`](internal/features/review/coherence_test.go)     |
| A link rule creates a link only when both files exist.                                          | [`TestConfig_LinkRules`](internal/features/review/coherence_test.go)                  |
| Secrets are not stored in plain text.                                                           | [`TestSecrets_EncryptedAndHashedAtRest`](internal/features/admin/admin_test.go)       |
| Local mode refuses a non-loopback address.                                                      | [`TestLocalMode_LoopbackOnly`](internal/http/server_test.go)                          |
| Each endpoint enforces its role table.                                                          | [`TestAuthz_EndpointRoleTable`](internal/http/authz_test.go)                          |
| A guest cannot edit or ask the AI.                                                              | [`TestGuest_Restrictions`](internal/hostauth/hostauth_test.go)                        |
| Anchors follow edits, or become detached. They never point at the wrong text.                   | [`TestAnchor_Reanchor`](internal/engine/anchor/reanchor_test.go)                      |
| Speccy never changes a doc without an accept.                                                   | [`TestSuggestFix_RequiresAccept`](internal/features/review/fix_test.go)               |
| CLI exit codes match SDD §12.2.                                                                 | [`TestCLI_ExitCodes`](cmd/speccy/review_test.go)                                      |
| `speccy review --summary` works with no server and no `speccy init`.                            | [`TestCLI_SummaryNoSetup`](cmd/speccy/review_test.go)                                 |
| In advisory mode, a verdict never fails the Action's job.                                       | [`TestAction_AdvisoryNeverFails`](internal/action/action_test.go)                     |
| Inline comments go only on changed lines, keep to the limit, and are not posted twice.          | [`TestAction_InlineComments`](internal/action/action_test.go)                         |
| Suggestion blocks are only for fixes that need no model.                                        | [`TestAction_SuggestionsDeterministicOnly`](internal/action/action_test.go)           |
