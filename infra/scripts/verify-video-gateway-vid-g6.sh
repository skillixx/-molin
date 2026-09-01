#!/usr/bin/env bash
set -Eeuo pipefail

# 仅创建本轮一次性MySQL与测试进程，不读取项目环境文件，不连接共享服务。
if [[ "${VIDEO_GATEWAY_G6_ISOLATED_MYSQL_APPROVED:-NO}" != YES ]]; then
  echo 'VIDEO_G6_MYSQL=APPROVAL_REQUIRED'
  exit 3
fi
for tool in docker openssl go awk; do
  command -v "$tool" >/dev/null || { echo 'VIDEO_G6_MYSQL=FAILED reason=tool_missing'; exit 2; }
done
repo_root="$(cd "$(dirname "$BASH_SOURCE")/../.." && pwd)"
focus="${VIDEO_GATEWAY_G6_TEST_FOCUS:-all}"
test_packages=(./internal/modules/auth/service ./internal/modules/iam/service ./internal/modules/token_gateway ./internal/modules/token_gateway/dto ./internal/modules/token_gateway/handler ./internal/modules/token_gateway/model ./internal/modules/token_gateway/repository ./internal/modules/token_gateway/service ./internal/modules/token_gateway/video)
required_tests='TestVideoG6IAMMySQLFreshPermissions,TestVideoG6HTTPMySQLCreateRetrieveReplay,TestVideoG6HTTPServiceRequiresRealDependencies,TestVideoG6AccessMySQLMatrix,TestVideoG6ModelContractExplicitRequirements,TestVideoG6EntitlementMySQLMatrix,TestVideoG6JobJSONSnapshot,TestVideoG6ContentHTTPFullAndSingleRange,TestVideoG6ContentHTTPRangeAndValidatorMatrix,TestVideoG6ContentHTTPRecoveryNeverAppendsJSON,TestVideoG6ContentHTTPStoreFailureIsLowSensitivity'
required_tests+=',TestVideoG6AutoQuoteSingleConnectionMySQL,TestVideoG6JWTAuthenticationFailsClosed,TestVideoG6QuoteRepeatableReadWinnerMySQL'
required_tests+=',TestVideoG6ListHTTPRequiresDependencies'
required_tests+=',TestVideoG6CatalogPublishedHTTPMySQL'
required_tests+=',TestVideoG6ModelSnapshotPreservesContract,TestVideoG6ModelSnapshotRejectsInvalidContract,TestVideoG6ModelContractPersistenceMySQL'
required_tests+=',TestVideoG6ModelDraftReasonBinding,TestVideoG6ModelDraftHTTPMySQL'
required_tests+=',TestVideoG6ModelDraftAdoptionHTTPMySQL'
required_tests+=',TestVideoG6ModelDraftLegacyQueryHTTPMySQL'
required_tests+=',TestVideoG6ModelPublicationHTTPMySQL'
required_tests+=',TestVideoG6ModelPublicationCommitUnknownMySQL,TestVideoG6ModelPublicationSQLFailureRollbackMySQL,TestVideoG6ModelPublicationPermissionAndMFAExpiryMySQL,TestVideoG6ModelPublicationConcurrentDefaultMySQL'
required_tests+=',TestVideoG6ProjectKeyHTTPMySQL,TestVideoG6ProjectKeyExplicitCapabilityAndRotation'
required_tests+=',TestVideoG6ProjectGrantHTTPMySQL'
required_tests+=',TestVideoG6ProjectKeyCommandCorruptionFailsClosedMySQL'
required_tests+=',TestVideoG6OpenAIInlineMultipartI2VMySQL,TestVideoG6OpenAIInlineFailureRecoveryMySQL,TestVideoG6OpenAIInlineFailureRecoveryMySQL/原件写入结果未知,TestVideoG6OpenAIInlineFailureRecoveryMySQL/Seal临时失败后接管,TestVideoG6OpenAIInlineFailureRecoveryMySQL/Seal成功回包丢失,TestVideoG6OpenAIInlineFinalAdmissionCleanupMySQL,TestVideoG6OpenAIInlineTCPReadLimitAndDisconnectMySQL,TestVideoG6OpenAIInlineDisconnectAfterCompleteMySQL,TestVideoG6OpenAIInlineGenerationCommitUnknownMySQL,TestVideoG6OpenAIInlineOwnerIsolationMySQL,TestVideoG6InlineReadLimiterBoundsUserMemory'
required_tests+=',TestVideoG6QueueAdmissionUserLimitMySQL,TestVideoG6QueueAdmissionConcurrentMySQL,TestVideoG6QueueAdmissionProjectAndGlobalLimitsMySQL,TestVideoG6QueueAdmissionProjectAndGlobalLimitsMySQL/Project第10个成功第11个拒绝,TestVideoG6QueueAdmissionProjectAndGlobalLimitsMySQL/Global第3个成功第4个拒绝,TestVideoG6QueueAdmissionFailureZeroFactsMySQL,TestVideoG6QueueAdmissionFrozenDefaults,TestVideoG6QueueAdmissionErrorEnvelopes'
required_tests+=',TestVideoG6RunningAdmissionMySQL,TestVideoG6RunningAdmissionConcurrentMySQL'
required_tests+=',TestVideoG6BudgetAdmissionProjectHardLimitMySQL,TestVideoG6BudgetAdmissionConcurrentMySQL,TestVideoG6BudgetTimezoneDayMonthBoundaryMySQL,TestVideoG6BudgetGenerationCommitUnknownMySQL,TestVideoG6BudgetLifecycleSettleReleaseAndRollbackMySQL,TestVideoG6BudgetLifecycleSettleReleaseAndRollbackMySQL/成功结算同步settled金额,TestVideoG6BudgetLifecycleSettleReleaseAndRollbackMySQL/明确Provider失败同步released,TestVideoG6BudgetLifecycleSettleReleaseAndRollbackMySQL/预算后置故障整笔回滚,TestVideoG6BudgetAdmissionErrorEnvelopes'
required_tests+=',TestVideoG6CompletedListsMySQLConcurrency'
required_tests+=',TestVideoG6RightsMySQLAcceptanceAndInvalidation'
required_tests+=',TestVideoG6I2VMySQLRightsQuoteHold'
required_tests+=',TestVideoG6I2VMySQLReplayAfterInputDeletion'
required_tests+=',TestVideoG6UploadMySQLSealCompleteReplay,TestVideoG6UploadMySQLRejectAndCancelRace'
required_tests+=',TestVideoG6UploadMySQLInterruptedRetryAndConcurrency,TestVideoG6UploadHTTPClosedRoutes'
required_tests+=',TestVideoG6UploadHTTPMySQLRoundtripAndI2V'
required_tests+=',TestVideoG6UploadMySQLRecoveryFences'
required_tests+=',TestVideoG6InputReadHTTPClosedRoutes,TestVideoG6InputMetadataMySQL'
required_tests+=',TestVideoG6SourceImagesMySQL'
required_tests+=',TestVideoG6ImportHTTPMySQL,TestVideoG6FixtureSequenceTracksAuxiliaryKeyMySQL'
required_tests+=',TestVideoG6TaskReadHTTPClosedRoutes'
required_tests+=',TestVideoG6TaskReadMySQLSettlementSnapshot,TestVideoG6TaskReadMySQLFinancialLifecycle'
required_tests+=',TestVideoG6InputDeferredDeleteMySQL,TestVideoG6InputDeleteReplayRRMySQL'
required_tests+=',TestVideoG6TaskReferenceAfterDeleteMySQL'
required_tests+=',TestVideoG6InputCleanupMySQLUpload'
required_tests+=',TestVideoG6ImportReadyRetentionMySQL,TestVideoG6InputCleanupMySQLImportHTTP'
required_tests+=',TestVideoG6InputCleanupMySQLBoundTasks'
required_tests+=',TestVideoG6ContentMySQLFinancialGate,TestVideoG6ContentHTTPMySQL,TestVideoG6ContentHTTPClosedRoute'
required_tests+=',TestVideoG6ContentHTTPApplicationErrorSingleEnvelope'
required_tests+=',TestVideoG6ContentStreamRevocationAndDeleteMySQL'
required_tests+=',TestVideoG6DownloadLimitsMySQL,TestVideoG6DownloadProjectFifthLeaseMySQL,TestVideoG6ContentHTTPBandwidth,TestVideoG6ContentHTTPLeaseDeadlineAndCancel,TestVideoG6ContentHTTPRealSlowClientTimeout'
required_tests+=',TestVideoG6DownloadRenewalRaceMySQL,TestVideoG6DownloadLeaseCommitUnknownMySQL'
required_tests+=',TestVideoG6PlayableMP4Probe,TestVideoG6PlayableContentHTTPMySQL,TestVideoG6MediaClockMatrix'
required_tests+=',TestVideoG6TaskCancelMySQL,TestVideoG6CancelHTTPClosedRoutes'
required_tests+=',TestVideoG6TaskCancelHTTPMySQL,TestVideoG6TaskCancelRollbackMySQL,TestVideoG6TaskCancelSubmittingMySQL'
required_tests+=',TestVideoG6TaskCancelDatabaseRetryMySQL'
required_tests+=',TestVideoG6MediaDeleteHTTPMySQL,TestVideoG6MediaDeleteClosedRoute'
required_tests+=',TestVideoG6MediaDeletePlanShape'
required_tests+=',TestVideoG6MediaDeleteRetainedCopyMySQL,TestVideoG6MediaDeleteConfirmationRollbackMySQL,TestVideoG6MediaDeletePrepareDeadlineMySQL'
required_tests+=',TestVideoG6MediaDeleteAssetRecoveryHTTPMySQL,TestVideoG6MediaDeleteOperationAfterPrepareMySQL,TestVideoG6MediaDeleteJWTRevokedBeforeDeleteHTTPMySQL'
required_tests+=',TestVideoG6MediaDeleteKeyExpiresDuringAuthorizationMySQL,TestPermissionFreshAuthorizationExpiry'
required_tests+=',TestVideoG6MediaDeleteEntitlementExpiryPathsMySQL,TestVideoG6MediaDeleteStoreAuthorizationDeadlineMySQL'
required_tests+=',TestVideoG6CallbackVerifierContract,TestVideoG6CallbackClosedRoute,TestVideoG6CallbackHTTPMySQL'
required_tests+=',TestVideoG6CallbackAtomicHTTPMySQL'
required_tests+=',TestVideoG6CallbackHistoricalIgnoredRRMySQL'
required_tests+=',TestVideoG6AdminTaskClosedRoute,TestVideoG6AdminTaskHTTPMySQL'
required_tests+=',TestVideoG6AdminListClosedRoute,TestVideoG6AdminListHTTPMySQL,TestVideoG6AdminListStatusRaceMySQL'
required_tests+=',TestVideoG6AdminListDisjointWalletsHTTPMySQL'
required_tests+=',TestVideoG6AdminInputListClosedRoute,TestVideoG6AdminInputListHTTPMySQL'
required_tests+=',TestVideoG6AdminOutputListClosedRoute,TestVideoG6AdminOutputListHTTPMySQL,TestVideoG6AdminSummaryClosedRoute,TestVideoG6AdminSummaryHTTPMySQL'
required_tests+=',TestVideoG6AdminReasonEncryption,TestVideoG6AdminCancelClosedRoute,TestVideoG6AdminCancelHTTPMySQL'
required_tests+=',TestVideoG6AdminCancelInvalidUTF8MySQL,TestVideoG6AdminCancelReasonKeyChangeMySQL,TestVideoG6AdminCancelAuditReadRetryMySQL'
required_tests+=',TestVideoG6AdminCancelConcurrentMySQL,TestVideoG6AdminCancelAtomicWritesMySQL,TestVideoG6AdminCancelCommitUnknownMySQL,TestVideoG6AdminCancelI2VLeaseMySQL,TestVideoG6AdminInputReasonBinding,TestVideoG6AdminInputQuarantineClosedRoute,TestVideoG6AdminInputQuarantineHTTPMySQL'
required_tests+=',TestVideoG6AdminOutputReasonBinding,TestVideoG6AdminOutputQuarantineClosedRoute,TestVideoG6AdminOutputQuarantineHTTPMySQL'
required_tests+=',TestAdminVerifyReadErrorCompatibility,TestIsAdminVerifyValidFailsClosedForInvalidTimeConfiguration'
required_tests+=',TestVideoG6CallbackLegacyGatewayMySQL,TestVideoGatewayCallbackReplayAndBodyConflict,TestVideoGatewayCallbackSuccessCarriesContentIntoFetch,TestVideoGatewayPollCallbackCancelCompetitionNeverRegresses'
test_filter='^TestVideoG6|^TestPermissionFreshAuthorizationExpiry$|^TestVideoGateway(Callback|PollCallback)|^Test(AdminVerifyReadErrorCompatibility|IsAdminVerifyValidFailsClosedForInvalidTimeConfiguration)$'
required_tests+=',TestVideoG6AssetLifecycleHTTPMySQL,TestVideoG6AssetLifecycleClosedRoute,TestVideoG6AssetLifecycleExpiryMySQL'
required_tests+=',TestVideoG6AssetDownloadHTTPMySQL,TestVideoG6AssetDownloadClosedRoutes'
required_tests+=',TestVideoG6AssetDownloadJWTRevocationMySQL'
required_tests+=',TestVideoG6AssetDownloadJWTExpiryMySQL,TestVideoG6AssetDownloadJWTDependencyMySQL,TestVideoG6AssetDownloadURLExpiryMySQL,TestVideoG6JWTInitialDeadline'
required_tests+=',TestVideoG6SavedCopyImmutable,TestVideoG6SavedCopyConcurrent,TestVideoG6SavedCapacityUnits'
required_tests+=',TestVideoG6SavedCoordinationMySQL'
required_tests+=',TestVideoG6AssetSaveHTTPMySQL,TestVideoG6AssetSaveClosedRoute'
required_tests+=',TestVideoG6AssetSaveSeparateStoreMySQL,TestVideoG6AssetSavePartialCopyMySQL'
required_tests+=',TestVideoG6AssetSaveConcurrentMySQL,TestVideoG6AssetSaveCapacityMySQL'
required_tests+=',TestVideoG6AssetSaveCommitExpiryMySQL'
required_tests+=',TestVideoG6AssetSaveCleanupMySQL'
required_tests+=',TestVideoG6AssetSaveCleanupRecoveryMySQL,TestVideoG6AssetSaveCleanupProtectionMySQL'
required_tests+=',TestVideoG6AssetSaveCleanupLateCopyMySQL'
required_tests+=',TestVideoG6AssetSaveCleanupIntentMySQL,TestVideoG6AssetSaveCleanupCommitUnknownMySQL'
required_tests+=',TestVideoG6SavedReadHTTPMySQL,TestVideoG6SavedReadClosedRoutes'
required_tests+=',TestVideoG6SavedReadSharedLimitsMySQL,TestVideoG6SavedReadJWTMySQL,TestVideoG6SavedReadSeparateStoreMySQL'
required_tests+=',TestVideoG6SavedReadPublicationClockMySQL,TestVideoG6SavedReadStreamRevocationMySQL'
required_tests+=',TestVideoG6SavedReadDatabaseFaultMySQL'
required_tests+=',TestVideoG6AssetSaveReattemptMySQL'
required_tests+=',TestVideoG6AssetSaveReattemptConcurrentMySQL'
required_tests+=',TestVideoG6AssetDeleteClosedRoute,TestVideoG6MediaDeletePlatformAssetHTTPMySQL,TestVideoG6MediaDeletePlanTargetBindingMySQL'
required_tests+=',TestVideoG6MediaDeleteReadTargetBindingMySQL'
required_tests+=',TestVideoG6AdminOutputReleaseClosedRoute,TestVideoG6AdminOutputReleaseHTTPMySQL,TestVideoG6AdminOutputReleaseReasonBinding'
required_tests+=',TestVideoG6AdminPollClosedRoute,TestVideoG6AdminPollHTTPMySQL,TestVideoG6AdminPollConcurrentMySQL,TestVideoG6AdminPollCommitUnknownMySQL,TestVideoG6AdminPollPostQueryRevocationMySQL,TestVideoG6AdminPollMFAExpiryMySQL'
required_tests+=',TestVideoG6ArchiveFenceRejectsUnprovenWriter,TestVideoG6ArchiveFenceMySQL,TestVideoG6ArchiveFenceExpiryMySQL'
required_tests+=',TestVideoG6ArchiveObjectFence,TestVideoG6ArchiveExecutorMySQL,TestVideoG6ArchiveExecutorFinalAuthorizationMySQL'
required_tests+=',TestVideoG6AdminArchiveClosedRoute,TestVideoG6AdminArchiveCommandSchema,TestVideoG6AdminArchiveHTTPMySQL'
required_tests+=',TestVideoG6AdminArchiveSafetyFailureMySQL,TestVideoG6AdminArchiveConcurrentMySQL,TestVideoG6AdminArchiveCommitUnknownMySQL'
required_tests+=',TestVideoG6AdminAdjustmentClosedRoute,TestVideoG6AdminAdjustmentHTTPMySQL'
case "$focus" in
  admin-adjustments) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway); required_tests='TestVideoG6AdminAdjustmentClosedRoute,TestVideoG6AdminAdjustmentHTTPMySQL,TestVideoG6AdminOutputReleaseHTTPMySQL'; test_filter="^(${required_tests//,/|})$" ;;
  legacy-adjustments) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG5AdjustmentMySQLAppendWalletAndReconcile,TestVideoG5AdjustmentMySQLConcurrencyRollbackAndMissingMovement'; test_filter="^(${required_tests//,/|})$" ;;
  admin-archive-safety) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6AdminArchiveSafetyFailureMySQL,TestVideoG6AdminArchiveHTTPMySQL,TestVideoG6ArchiveExecutorFinalAuthorizationMySQL'; test_filter="^(${required_tests//,/|})$" ;;
  admin-archive) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway ./internal/modules/token_gateway/video); required_tests='TestVideoG6AdminArchiveClosedRoute,TestVideoG6AdminArchiveCommandSchema,TestVideoG6AdminArchiveHTTPMySQL,TestVideoG6AdminArchiveConcurrentMySQL,TestVideoG6AdminArchiveCommitUnknownMySQL,TestVideoG6ArchiveExecutorMySQL,TestVideoG6ArchiveExecutorFinalAuthorizationMySQL,TestVideoG6AdminPollHTTPMySQL,TestVideoG6ArchiveObjectFence'; test_filter="^(${required_tests//,/|})$" ;;
  archive-recovery) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6AdminArchiveConcurrentMySQL,TestVideoG6AdminArchiveCommitUnknownMySQL,TestVideoG6AdminArchiveHTTPMySQL'; test_filter="^(${required_tests//,/|})$" ;;
  archive-executor) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/video); required_tests='TestVideoG6ArchiveObjectFence,TestVideoG6ArchiveExecutorMySQL,TestVideoG6ArchiveExecutorFinalAuthorizationMySQL,TestVideoG6ArchiveFenceMySQL,TestVideoG6ArchiveFenceExpiryMySQL,TestVideoGatewayRunsFakeAsyncClosureForTextAndImage,TestVideoG6SavedCopyImmutable'; test_filter="^(${required_tests//,/|})$" ;;
  archive-fence) test_packages=(./internal/modules/token_gateway/repository ./internal/modules/token_gateway/service ./internal/modules/token_gateway/video); required_tests='TestVideoG6ArchiveFenceRejectsUnprovenWriter,TestVideoG6ArchiveFenceMySQL,TestVideoG6ArchiveFenceExpiryMySQL,TestVideoG6AdminPollHTTPMySQL,TestVideoG6CallbackHTTPMySQL,TestVideoGatewayRunsFakeAsyncClosureForTextAndImage'; test_filter="^(${required_tests//,/|})$" ;;
  admin-poll) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway ./internal/modules/token_gateway/video); required_tests='TestVideoG6AdminPollClosedRoute,TestVideoG6AdminPollHTTPMySQL,TestVideoG6AdminPollConcurrentMySQL,TestVideoG6AdminPollCommitUnknownMySQL,TestVideoG6AdminPollPostQueryRevocationMySQL,TestVideoG6AdminPollMFAExpiryMySQL,TestVideoG6AdminOutputReleaseHTTPMySQL,TestVideoG6AdminOutputReleaseReasonBinding,TestVideoG6AdminOutputQuarantineHTTPMySQL,TestVideoGatewayPollCallbackCancelCompetitionNeverRegresses,TestVideoPollQueuedRetainsSubmittedTask'; test_filter="^(${required_tests//,/|})$" ;;
  admin-output-release) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway); required_tests='TestVideoG6AdminOutputReleaseClosedRoute,TestVideoG6AdminOutputReleaseHTTPMySQL,TestVideoG6AdminOutputReleaseReasonBinding,TestVideoG6AdminOutputQuarantineHTTPMySQL,TestVideoG6AdminReasonEncryption,TestVideoG6AdminOutputReasonBinding,TestVideoG6AssetSaveHTTPMySQL,TestVideoG6SavedReadHTTPMySQL'; test_filter="^(${required_tests//,/|})$" ;;
  admin-output-quarantine) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway); required_tests='TestVideoG6AdminReasonEncryption,TestVideoG6AdminInputReasonBinding,TestVideoG6AdminOutputReasonBinding,TestVideoG6AdminOutputQuarantineClosedRoute,TestVideoG6AdminOutputQuarantineHTTPMySQL,TestVideoG6AdminOutputListHTTPMySQL,TestVideoG6AssetLifecycleHTTPMySQL,TestVideoG6AssetDownloadHTTPMySQL,TestVideoG6AssetSaveHTTPMySQL,TestVideoG6SavedReadHTTPMySQL,TestVideoG6MediaDeleteHTTPMySQL'; test_filter="^(${required_tests//,/|})$" ;;
  admin-cancel-i2v) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6AdminCancelI2VLeaseMySQL'; test_filter='^TestVideoG6AdminCancelI2VLeaseMySQL$' ;;
  admin-mutations) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway); required_tests='TestVideoG6AdminReasonEncryption,TestVideoG6AdminInputReasonBinding,TestVideoG6AdminCancelClosedRoute,TestVideoG6AdminCancelHTTPMySQL,TestVideoG6AdminCancelInvalidUTF8MySQL,TestVideoG6AdminCancelReasonKeyChangeMySQL,TestVideoG6AdminCancelAuditReadRetryMySQL,TestVideoG6AdminCancelConcurrentMySQL,TestVideoG6AdminCancelAtomicWritesMySQL,TestVideoG6AdminCancelCommitUnknownMySQL,TestVideoG6AdminCancelI2VLeaseMySQL,TestVideoG6AdminInputQuarantineClosedRoute,TestVideoG6AdminInputQuarantineHTTPMySQL,TestVideoG6AdminInputListHTTPMySQL,TestVideoG6InputMetadataMySQL'; test_filter="^(${required_tests//,/|})$" ;;
  admin-read) test_packages=(./internal/modules/auth/service ./internal/modules/token_gateway/service ./internal/modules/token_gateway); required_tests='TestVideoG6AdminTaskClosedRoute,TestVideoG6AdminTaskHTTPMySQL,TestVideoG6AdminListClosedRoute,TestVideoG6AdminListHTTPMySQL,TestVideoG6AdminListStatusRaceMySQL,TestVideoG6AdminListDisjointWalletsHTTPMySQL,TestVideoG6AdminInputListClosedRoute,TestVideoG6AdminInputListHTTPMySQL,TestVideoG6AdminOutputListClosedRoute,TestVideoG6AdminOutputListHTTPMySQL,TestVideoG6AdminSummaryClosedRoute,TestVideoG6AdminSummaryHTTPMySQL,TestVideoG6AdminReasonEncryption,TestVideoG6AdminCancelClosedRoute,TestVideoG6AdminCancelHTTPMySQL,TestVideoG6AdminCancelInvalidUTF8MySQL,TestVideoG6AdminCancelReasonKeyChangeMySQL,TestVideoG6AdminCancelAuditReadRetryMySQL,TestAdminVerifyReadErrorCompatibility,TestIsAdminVerifyValidFailsClosedForInvalidTimeConfiguration'; test_filter='^TestVideoG6Admin|^Test(AdminVerifyReadErrorCompatibility|IsAdminVerifyValidFailsClosedForInvalidTimeConfiguration)$' ;;
  callbacks) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway ./internal/modules/token_gateway/video); required_tests='TestVideoG6CallbackVerifierContract,TestVideoG6CallbackClosedRoute,TestVideoG6CallbackHTTPMySQL,TestVideoG6CallbackAtomicHTTPMySQL,TestVideoG6CallbackHistoricalIgnoredRRMySQL,TestVideoG6CallbackLegacyGatewayMySQL,TestVideoGatewayCallbackReplayAndBodyConflict,TestVideoGatewayCallbackSuccessCarriesContentIntoFetch,TestVideoGatewayPollCallbackCancelCompetitionNeverRegresses'; test_filter='^TestVideoG6Callback|^TestVideoGateway(Callback|PollCallback)' ;;
  asset-delete) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway); required_tests='TestVideoG6AssetDeleteClosedRoute,TestVideoG6MediaDeletePlatformAssetHTTPMySQL,TestVideoG6MediaDeletePlanTargetBindingMySQL,TestVideoG6MediaDeleteReadTargetBindingMySQL,TestVideoG6MediaDeleteHTTPMySQL'; test_filter='^TestVideoG6(AssetDeleteClosedRoute|MediaDeletePlatformAssetHTTPMySQL|MediaDeletePlanTargetBindingMySQL|MediaDeleteReadTargetBindingMySQL|MediaDeleteHTTPMySQL)$' ;;
  delete-auth) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6MediaDeleteAssetRecoveryHTTPMySQL,TestVideoG6MediaDeleteOperationAfterPrepareMySQL,TestVideoG6MediaDeleteJWTRevokedBeforeDeleteHTTPMySQL,TestVideoG6MediaDeleteHTTPMySQL,TestVideoG6ImportHTTPMySQL,TestVideoG6TaskCancelHTTPMySQL,TestVideoG6SavedReadStreamRevocationMySQL'; test_filter='^TestVideoG6(MediaDeleteAssetRecoveryHTTPMySQL|MediaDeleteOperationAfterPrepareMySQL|MediaDeleteJWTRevokedBeforeDeleteHTTPMySQL|MediaDeleteHTTPMySQL|ImportHTTPMySQL|TaskCancelHTTPMySQL|SavedReadStreamRevocationMySQL)$' ;;
  fixture-sequence) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6FixtureSequenceTracksAuxiliaryKeyMySQL,TestVideoG6TaskCancelRollbackMySQL,TestVideoG6ImportHTTPMySQL'; test_filter='^TestVideoG6(FixtureSequenceTracksAuxiliaryKeyMySQL|TaskCancelRollbackMySQL|ImportHTTPMySQL)$' ;;
  delete-expiry) test_packages=(./internal/modules/token_gateway/service ./internal/modules/iam/service); required_tests='TestVideoG6MediaDeleteKeyExpiresDuringAuthorizationMySQL,TestVideoG6MediaDeleteOperationAfterPrepareMySQL,TestVideoG6MediaDeleteJWTRevokedBeforeDeleteHTTPMySQL,TestVideoG6MediaDeleteHTTPMySQL,TestVideoG6AccessMySQLMatrix,TestVideoG6EntitlementMySQLMatrix,TestPermissionFreshAuthorizationExpiry,TestVideoG6IAMMySQLFreshPermissions'; test_filter='^TestVideoG6(MediaDeleteKeyExpiresDuringAuthorizationMySQL|MediaDeleteOperationAfterPrepareMySQL|MediaDeleteJWTRevokedBeforeDeleteHTTPMySQL|MediaDeleteHTTPMySQL|AccessMySQLMatrix|EntitlementMySQLMatrix|IAMMySQLFreshPermissions)$|^TestPermissionFreshAuthorizationExpiry$' ;;
  save-migration) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoMigration91HistoryMySQL'; test_filter='^TestVideoMigration91HistoryMySQL$' ;;
  save-reattempt) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6AssetSaveReattemptMySQL,TestVideoG6AssetSaveReattemptConcurrentMySQL,TestVideoG6SavedCoordinationMySQL,TestVideoG6AssetSaveHTTPMySQL,TestVideoG6SavedReadSharedLimitsMySQL,TestVideoG6ContentMySQLFinancialGate'; test_filter='^TestVideoG6(AssetSaveReattempt.*MySQL|SavedCoordinationMySQL|AssetSaveHTTPMySQL|SavedReadSharedLimitsMySQL|ContentMySQLFinancialGate)$' ;;
  saved-clock) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6SavedReadPublicationClockMySQL'; test_filter='^TestVideoG6SavedReadPublicationClockMySQL$' ;;
  model-draft) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler); required_tests='TestVideoG6ModelDraftReasonBinding,TestVideoG6ModelDraftHTTPMySQL,TestVideoG6ModelDraftAdoptionHTTPMySQL,TestVideoG6ModelDraftLegacyQueryHTTPMySQL,TestVideoG6CatalogPublishedHTTPMySQL,TestDeleteModelAuditFailurePreventsWrite,TestUpdateModelRejectsUnknownFieldBeforeWrite'; test_filter="^(${required_tests//,/|})$" ;;
  project-key-idempotency) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler ./internal/modules/token_gateway/repository); required_tests='TestVideoG6ProjectKeyHTTPMySQL,TestVideoG6ProjectKeyCommandCorruptionFailsClosedMySQL,TestVideoG6ProjectGrantHTTPMySQL,TestProjectKeyLifecycleWritesAuditWithoutPlaintext'; test_filter="^(${required_tests//,/|})$" ;;
  inline-i2v) test_packages=(./internal/modules/token_gateway ./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler); required_tests='TestVideoG6OpenAIInlineMultipartI2VMySQL,TestVideoG6OpenAIInlineFailureRecoveryMySQL,TestVideoG6OpenAIInlineFailureRecoveryMySQL/原件写入结果未知,TestVideoG6OpenAIInlineFailureRecoveryMySQL/Seal临时失败后接管,TestVideoG6OpenAIInlineFailureRecoveryMySQL/Seal成功回包丢失,TestVideoG6OpenAIInlineFinalAdmissionCleanupMySQL,TestVideoG6OpenAIInlineTCPReadLimitAndDisconnectMySQL,TestVideoG6OpenAIInlineDisconnectAfterCompleteMySQL,TestVideoG6OpenAIInlineGenerationCommitUnknownMySQL,TestVideoG6OpenAIInlineOwnerIsolationMySQL,TestVideoG6I2VMySQLRightsQuoteHold,TestVideoG6UploadMySQLSealCompleteReplay,TestVideoG6HTTPMySQLCreateRetrieveReplay'; test_filter="^(${required_tests//,/|})$" ;;
  inline-commit) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6OpenAIInlineGenerationCommitUnknownMySQL,TestVideoG6OpenAIInlineDisconnectAfterCompleteMySQL'; test_filter="^(${required_tests//,/|})$" ;;
  queue-admission) test_packages=(./internal/modules/token_gateway ./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler); required_tests='TestVideoG6QueueAdmissionUserLimitMySQL,TestVideoG6QueueAdmissionConcurrentMySQL,TestVideoG6QueueAdmissionProjectAndGlobalLimitsMySQL,TestVideoG6QueueAdmissionProjectAndGlobalLimitsMySQL/Project第10个成功第11个拒绝,TestVideoG6QueueAdmissionProjectAndGlobalLimitsMySQL/Global第3个成功第4个拒绝,TestVideoG6QueueAdmissionFailureZeroFactsMySQL,TestVideoG6QueueAdmissionFrozenDefaults,TestVideoG6QueueAdmissionErrorEnvelopes,TestVideoG6I2VMySQLRightsQuoteHold,TestVideoG6HTTPMySQLCreateRetrieveReplay'; test_filter="^(${required_tests//,/|})$" ;;
  running-admission) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/video); required_tests='TestVideoG6RunningAdmissionMySQL,TestVideoG6RunningAdmissionConcurrentMySQL,TestVideoG6TaskCancelSubmittingMySQL,TestVideoGatewayRunsFakeAsyncClosureForTextAndImage'; test_filter="^(${required_tests//,/|})$" ;;
  budget-admission) test_packages=(./internal/modules/token_gateway ./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler); required_tests='TestVideoG6BudgetAdmissionProjectHardLimitMySQL,TestVideoG6BudgetAdmissionConcurrentMySQL,TestVideoG6BudgetTimezoneDayMonthBoundaryMySQL,TestVideoG6BudgetGenerationCommitUnknownMySQL,TestVideoG6BudgetLifecycleSettleReleaseAndRollbackMySQL,TestVideoG6BudgetLifecycleSettleReleaseAndRollbackMySQL/成功结算同步settled金额,TestVideoG6BudgetLifecycleSettleReleaseAndRollbackMySQL/明确Provider失败同步released,TestVideoG6BudgetLifecycleSettleReleaseAndRollbackMySQL/预算后置故障整笔回滚,TestVideoG6BudgetAdmissionErrorEnvelopes,TestVideoG6QueueAdmissionUserLimitMySQL,TestVideoG6I2VMySQLRightsQuoteHold,TestVideoG6HTTPMySQLCreateRetrieveReplay'; test_filter="^(${required_tests//,/|})$" ;;
  project-grant) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler); required_tests='TestVideoG6ProjectGrantHTTPMySQL,TestVideoG6ProjectKeyHTTPMySQL,TestVideoG6AccessMySQLMatrix,TestVideoG6CatalogPublishedHTTPMySQL'; test_filter="^(${required_tests//,/|})$" ;;
  project-key) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler ./internal/modules/token_gateway/repository); required_tests='TestVideoG6ProjectKeyHTTPMySQL,TestVideoG6ProjectKeyExplicitCapabilityAndRotation,TestVideoG6AccessMySQLMatrix,TestVideoG6CatalogPublishedHTTPMySQL,TestProjectKeyRotateRevokesOldKeyAndReturnsSecretOnce,TestProjectKeyDefaultsToDenyAllAllowlistAndStoresOnlyHash,TestProjectKeyAllModeRequiresExplicitEmptyModelList'; test_filter="^(${required_tests//,/|})$" ;;
  model-publication) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler); required_tests='TestVideoG6ModelPublicationHTTPMySQL,TestVideoG6ModelPublicationCommitUnknownMySQL,TestVideoG6ModelPublicationSQLFailureRollbackMySQL,TestVideoG6ModelPublicationPermissionAndMFAExpiryMySQL,TestVideoG6ModelPublicationConcurrentDefaultMySQL,TestVideoG6ModelDraftAdoptionHTTPMySQL,TestVideoG6CatalogPublishedHTTPMySQL'; test_filter="^(${required_tests//,/|})$" ;;
  model-default) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6ModelPublicationConcurrentDefaultMySQL,TestVideoG6ModelPublicationHTTPMySQL'; test_filter="^(${required_tests//,/|})$" ;;
  legacy-model-publication) test_packages=(./internal/modules/token_gateway/repository); required_tests='TestG5MySQLIntegration'; test_filter='^TestG5MySQLIntegration$' ;;
  model-contract) test_packages=(./internal/modules/token_gateway/model ./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler); required_tests='TestVideoG6ModelSnapshotPreservesContract,TestVideoG6ModelSnapshotRejectsInvalidContract,TestVideoG6ModelContractExplicitRequirements,TestVideoG6ModelContractPersistenceMySQL,TestVideoG6CatalogPublishedHTTPMySQL,TestFetchOpenAIChatModels_OnlyChat,TestBuildOpenAIModelList,TestBuildOpenAIModelList_Empty,TestFilterModelsByKeyAccess_EmptyAllowlistDeniesAll'; test_filter="^(${required_tests//,/|})$" ;;
  catalog) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler); required_tests='TestVideoG6CatalogPublishedHTTPMySQL,TestFetchOpenAIChatModels_OnlyChat,TestBuildOpenAIModelList,TestBuildOpenAIModelList_Empty,TestFilterModelsByKeyAccess_EmptyAllowlistDeniesAll'; test_filter="^(${required_tests//,/|})$" ;;
  all) ;;
  saved-read) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway ./internal/modules/token_gateway/handler); required_tests='TestVideoG6SavedReadHTTPMySQL,TestVideoG6SavedReadClosedRoutes,TestVideoG6SavedReadSharedLimitsMySQL,TestVideoG6SavedReadJWTMySQL,TestVideoG6SavedReadSeparateStoreMySQL,TestVideoG6SavedReadPublicationClockMySQL,TestVideoG6SavedReadStreamRevocationMySQL,TestVideoG6SavedReadDatabaseFaultMySQL,TestVideoG6SavedCoordinationMySQL,TestVideoG6AssetSaveHTTPMySQL,TestVideoG6ContentHTTPMySQL,TestVideoG6ContentHTTPRangeAndValidatorMatrix'; test_filter='^TestVideoG6(SavedRead|SavedCoordinationMySQL|AssetSaveHTTPMySQL|ContentHTTP)' ;;
  save-cleanup) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6AssetSaveCleanupMySQL,TestVideoG6AssetSaveCleanupRecoveryMySQL,TestVideoG6AssetSaveCleanupProtectionMySQL,TestVideoG6AssetSaveCleanupLateCopyMySQL,TestVideoG6AssetSaveCleanupIntentMySQL,TestVideoG6AssetSaveCleanupCommitUnknownMySQL,TestVideoG6AssetSavePartialCopyMySQL,TestVideoG6AssetSaveHTTPMySQL'; test_filter='^TestVideoG6(AssetSaveCleanup.*|AssetSavePartialCopy|AssetSaveHTTP)MySQL$' ;;
  asset-save) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway ./internal/modules/token_gateway/video); required_tests='TestVideoG6AssetSaveHTTPMySQL,TestVideoG6AssetSaveClosedRoute,TestVideoG6AssetSaveSeparateStoreMySQL,TestVideoG6AssetSavePartialCopyMySQL,TestVideoG6AssetSaveConcurrentMySQL,TestVideoG6AssetSaveCapacityMySQL,TestVideoG6AssetSaveCommitExpiryMySQL,TestVideoG6SavedCopyImmutable,TestVideoG6SavedCopyConcurrent,TestVideoG6SavedCapacityUnits,TestVideoG6SavedCoordinationMySQL,TestVideoG6AssetLifecycleHTTPMySQL,TestVideoG6MediaDeleteHTTPMySQL'; test_filter='^TestVideoG6(AssetSave|Saved|AssetLifecycleHTTPMySQL|MediaDeleteHTTPMySQL)' ;;
  asset-save-expiry) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6AssetSaveCommitExpiryMySQL'; test_filter='^TestVideoG6AssetSaveCommitExpiryMySQL$' ;;
  save-foundation) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/video); required_tests='TestVideoG6SavedCopyImmutable,TestVideoG6SavedCopyConcurrent,TestVideoG6SavedCapacityUnits,TestVideoG6SavedCoordinationMySQL,TestVideoG6AssetLifecycleHTTPMySQL'; test_filter='^TestVideoG6(Saved|AssetLifecycleHTTPMySQL)' ;;
  asset-download) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway ./internal/modules/token_gateway/handler); required_tests='TestVideoG6AssetDownloadHTTPMySQL,TestVideoG6AssetDownloadClosedRoutes,TestVideoG6AssetDownloadJWTRevocationMySQL,TestVideoG6AssetDownloadJWTExpiryMySQL,TestVideoG6AssetDownloadJWTDependencyMySQL,TestVideoG6AssetDownloadURLExpiryMySQL,TestVideoG6JWTInitialDeadline,TestVideoG6AssetLifecycleHTTPMySQL,TestVideoG6AssetLifecycleClosedRoute,TestVideoG6AssetLifecycleExpiryMySQL,TestVideoG6ContentHTTPMySQL,TestVideoG6ContentHTTPFullAndSingleRange,TestVideoG6ContentHTTPRangeAndValidatorMatrix'; test_filter='^TestVideoG6(AssetDownload|AssetLifecycle|ContentHTTP|JWTInitialDeadline)' ;;
  asset-lifecycle) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway); required_tests='TestVideoG6AssetLifecycleHTTPMySQL,TestVideoG6AssetLifecycleClosedRoute,TestVideoG6AssetLifecycleExpiryMySQL'; test_filter='^TestVideoG6AssetLifecycle' ;;
  media-delete) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway); required_tests='TestVideoG6MediaDeleteHTTPMySQL,TestVideoG6MediaDeleteClosedRoute,TestVideoG6MediaDeletePlanShape,TestVideoG6MediaDeleteRetainedCopyMySQL,TestVideoG6MediaDeleteConfirmationRollbackMySQL,TestVideoG6MediaDeletePrepareDeadlineMySQL'; test_filter='^TestVideoG6MediaDelete' ;;
  legacy-cancel) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG5CancelMySQLRejectsInconsistentFacts,TestVideoG5CancelMySQLRacesSubmitting,TestVideoG5CancelMySQLReplayRejectsExtraUsage,TestVideoG5CancelMySQLRollbackEveryWrite,TestVideoG5CancelMySQLQueuedOneRelease,TestVideoG5CancelMySQLRefundWinnerPreventsGatewaySubmit,TestVideoG5CancelMySQLIntentCASAndIsolation,TestVideoG5CancelMySQLInflightSubmitKeepsBinding,TestVideoG5CancelMySQLInvalidOrNonzeroConfirmation,TestVideoG5CancelMySQLZeroCostAloneCannotAuthorizeRelease,TestVideoG5CancelMySQLContradictoryInflightReplies,TestVideoG5CancelMySQLLateConflictBlocksSettlementAndRead,TestVideoG5CancelMySQLLatePollSuccessCannotHideConflict,TestVideoG5CancelMySQLSubmittedOutcomes'; test_filter='^TestVideoG5Cancel' ;;
  cancel) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway); required_tests='TestVideoG6TaskCancelMySQL,TestVideoG6CancelHTTPClosedRoutes,TestVideoG6TaskCancelHTTPMySQL,TestVideoG6TaskCancelRollbackMySQL,TestVideoG6TaskCancelSubmittingMySQL,TestVideoG6TaskCancelDatabaseRetryMySQL'; test_filter='^TestVideoG6(TaskCancel|CancelHTTP)' ;;
  playable) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/video); required_tests='TestVideoG6PlayableMP4Probe,TestVideoG6PlayableContentHTTPMySQL,TestVideoG6MediaClockMatrix'; test_filter='^TestVideoG6(Playable|MediaClock)' ;;
  download) test_packages=(./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler); required_tests='TestVideoG6DownloadLimitsMySQL,TestVideoG6DownloadProjectFifthLeaseMySQL,TestVideoG6DownloadRenewalRaceMySQL,TestVideoG6DownloadLeaseCommitUnknownMySQL,TestVideoG6ContentMySQLFinancialGate,TestVideoG6ContentHTTPMySQL,TestVideoG6ContentStreamRevocationAndDeleteMySQL,TestVideoG6ContentHTTPBandwidth,TestVideoG6ContentHTTPLeaseDeadlineAndCancel,TestVideoG6ContentHTTPRealSlowClientTimeout'; test_filter='^TestVideoG6(Download|Content)' ;;
  content) test_packages=(./internal/modules/token_gateway ./internal/modules/token_gateway/service ./internal/modules/token_gateway/handler); required_tests='TestVideoG6ContentMySQLFinancialGate,TestVideoG6ContentHTTPMySQL,TestVideoG6ContentHTTPClosedRoute,TestVideoG6ContentHTTPFullAndSingleRange,TestVideoG6ContentHTTPRangeAndValidatorMatrix,TestVideoG6ContentHTTPRecoveryNeverAppendsJSON,TestVideoG6ContentHTTPStoreFailureIsLowSensitivity,TestVideoG6ContentHTTPApplicationErrorSingleEnvelope'; test_filter='^TestVideoG6Content' ;;
  input-cleanup) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6InputCleanupMySQLUpload,TestVideoG6ImportReadyRetentionMySQL,TestVideoG6InputCleanupMySQLImportHTTP,TestVideoG6InputCleanupMySQLBoundTasks'; test_filter='^TestVideoG6(InputCleanupMySQL|ImportReadyRetentionMySQL)' ;;
  task-reference) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6TaskReferenceAfterDeleteMySQL'; test_filter='^TestVideoG6TaskReferenceAfterDeleteMySQL$' ;;
  input-delete) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6InputDeferredDeleteMySQL,TestVideoG6InputDeleteReplayRRMySQL'; test_filter='^TestVideoG6Input(DeferredDelete|DeleteReplayRR)MySQL$' ;;
  imports) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6SourceImagesMySQL'; test_filter='^TestVideoG6SourceImagesMySQL$' ;;
  task-read) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6TaskReadMySQLSettlementSnapshot,TestVideoG6TaskReadMySQLFinancialLifecycle,TestVideoG6CompletedListsMySQLConcurrency,TestVideoG6ImportHTTPMySQL'; test_filter='^TestVideoG6(TaskReadMySQL.*|CompletedListsMySQLConcurrency|ImportHTTPMySQL)$' ;;
  http) test_packages=(./internal/modules/token_gateway); required_tests='TestVideoG6HTTPMySQLCreateRetrieveReplay' ;;
  connection) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6AutoQuoteSingleConnectionMySQL'; test_filter='^TestVideoG6AutoQuoteSingleConnectionMySQL$' ;;
  list) test_packages=(./internal/modules/token_gateway ./internal/modules/token_gateway/service); required_tests='TestVideoG6HTTPMySQLCreateRetrieveReplay,TestVideoG6ListHTTPRequiresDependencies,TestVideoG6CompletedListsMySQLConcurrency'; test_filter='^TestVideoG6(HTTPMySQLCreateRetrieveReplay|ListHTTPRequiresDependencies|CompletedListsMySQLConcurrency)$' ;;
  rights) test_packages=(./internal/modules/token_gateway ./internal/modules/token_gateway/service); required_tests='TestVideoG6HTTPMySQLCreateRetrieveReplay,TestVideoG6RightsMySQLAcceptanceAndInvalidation'; test_filter='^TestVideoG6(HTTPMySQLCreateRetrieveReplay|RightsMySQLAcceptanceAndInvalidation)$' ;;
  i2v) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6I2VMySQLRightsQuoteHold,TestVideoG6I2VMySQLReplayAfterInputDeletion'; test_filter='^TestVideoG6I2VMySQL' ;;
  upload) test_packages=(./internal/modules/token_gateway/service); required_tests='TestVideoG6UploadMySQLSealCompleteReplay,TestVideoG6UploadMySQLRejectAndCancelRace,TestVideoG6UploadMySQLInterruptedRetryAndConcurrency,TestVideoG6UploadMySQLRecoveryFences'; test_filter='^TestVideoG6UploadMySQL' ;;
  inputs) test_packages=(./internal/modules/token_gateway ./internal/modules/token_gateway/service); required_tests='TestVideoG6InputReadHTTPClosedRoutes,TestVideoG6InputMetadataMySQL,TestVideoG6UploadHTTPMySQLRoundtripAndI2V'; test_filter='^TestVideoG6(InputReadHTTPClosedRoutes|InputMetadataMySQL|UploadHTTPMySQLRoundtripAndI2V)$' ;;
  *) echo 'VIDEO_G6_MYSQL=FAILED reason=invalid_focus'; exit 2 ;;
esac
database_name='molin_video_g6_contract'
if [[ "$focus" == legacy-cancel || "$focus" == legacy-adjustments || "$focus" == legacy-model-publication ]]; then database_name='molin_video_g5_contract'; fi
suffix="$(openssl rand -hex 8)"
network_name="molin-video-g6-$suffix"
container_name="molin-video-g6-mysql-$suffix"
build_name="molin-video-g6-build-$suffix"
network_id=''
container_id=''
build_id=''
# 仅复用本Goal和固定Go镜像的编译缓存；测试仍-count=1实际执行，缓存不包含数据库或凭据。
cache_volume='molin-video-g6-buildcache-908f8ff2ec29-v1'
root_password="$(openssl rand -hex 24)"
mysql_image='sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b'
go_image='sha256:908f8ff2ec296df2f349563072c7925775cd28b50361a52ed834a8a37399b9bf'
docker image inspect "$mysql_image" >/dev/null
docker image inspect "$go_image" >/dev/null
docker_repo="$repo_root"
docker_mod="$(go env GOMODCACHE)"
if command -v cygpath >/dev/null; then
  docker_repo="$(cygpath -w "$repo_root")"
  docker_mod="$(cygpath -w "$docker_mod")"
fi

# 只回收由本进程创建并取得精确ID的临时资源；不按前缀搜索或停止其他容器。
cleanup() {
  [[ -z "$build_id" ]] || docker rm -f "$build_id" >/dev/null 2>&1 || true
  [[ -z "$container_id" ]] || docker rm -f "$container_id" >/dev/null 2>&1 || true
  [[ -z "$network_id" ]] || docker network rm "$network_id" >/dev/null 2>&1 || true
  # 编译缓存留给后续本Goal复验；临时数据库、构建容器和网络仍每次精确回收。
}
trap cleanup EXIT
network_id="$(docker network create --internal --label molin.goal=VID-G6 "$network_name")"
if ! docker volume inspect "$cache_volume" >/dev/null 2>&1; then
  docker volume create --label molin.goal=VID-G6 --label molin.purpose=vid-g6-go-build-cache --label "molin.go-image=$go_image" "$cache_volume" >/dev/null
fi
[[ "$(docker volume inspect --format '{{ index .Labels "molin.goal" }}' "$cache_volume")" == VID-G6 \
  && "$(docker volume inspect --format '{{ index .Labels "molin.purpose" }}' "$cache_volume")" == vid-g6-go-build-cache \
  && "$(docker volume inspect --format '{{ index .Labels "molin.go-image" }}' "$cache_volume")" == "$go_image" ]] \
  || { echo 'VIDEO_G6_MYSQL=FAILED reason=cache_ownership'; exit 2; }
echo 'VIDEO_G6_BUILD_CACHE=VERIFIED test_result_cache=OFF'
container_id="$(docker run -d --pull=never --network "$network_id" --network-alias mysql --name "$container_name" \
  --label molin.goal=VID-G6 --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g \
  -e "MYSQL_ROOT_PASSWORD=$root_password" -e "MYSQL_DATABASE=$database_name" \
  "$mysql_image" --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci)"
mysql_exec() {
  docker exec -i -e "MYSQL_PWD=$root_password" "$container_id" mysql --no-defaults --protocol=socket \
    --default-character-set=utf8mb4 -uroot --database="$database_name" --batch --skip-column-names "$@"
}
ready=0
for _ in $(seq 1 90); do
  if mysql_exec -e 'SELECT 1' >/dev/null 2>&1; then ready=$((ready+1)); else ready=0; fi
  [[ "$ready" -ge 2 ]] && break
  sleep 1
done
[[ "$ready" -ge 2 ]] || { echo 'VIDEO_G6_MYSQL=FAILED reason=mysql_not_ready'; exit 2; }
# 先复制Git清单内源码，再启动编译；避免实时挂载读到开发中的红绿中间态。
# 依赖缓存仍只读，HTTP仅在容器回环监听，没有宿主或公网端口。
migration_paths=("$repo_root"/server/migrations/*.up.sql)
mysql_test_env=(-e MOLIN_VIDEO_G6_ISOLATED=YES -e "MOLIN_VIDEO_G6_MYSQL_DSN=root:$root_password@tcp(mysql:3306)/$database_name?charset=utf8mb4&parseTime=true&loc=UTC")
if [[ "$focus" == save-migration ]]; then
  mysql_test_env+=(-e MOLIN_VIDEO_G6_MIGRATION91=YES)
fi
if [[ "$focus" == legacy-cancel || "$focus" == legacy-adjustments ]]; then
  # 旧G5辅助函数仍要求自己的专用库名；只切换本次临时库，不读取项目或共享数据库。
  mysql_test_env=(-e MOLIN_VIDEO_G5_ISOLATED=YES -e "MOLIN_VIDEO_G5_MYSQL_DSN=root:$root_password@tcp(mysql:3306)/$database_name?charset=utf8mb4&parseTime=true&loc=UTC")
fi
if [[ "$focus" == legacy-model-publication ]]; then
  # 只装截至77号的历史schema；不能让102新增列掩盖旧Chat发布兼容问题。
  mysql_test_env=(-e G5_ISOLATED_TEST=YES -e "G5_MYSQL_DSN=root:$root_password@tcp(mysql:3306)/$database_name?charset=utf8mb4&parseTime=true&loc=UTC")
fi
build_id="$(MSYS_NO_PATHCONV=1 docker create --pull=never --network "$network_id" --name "$build_name" --label molin.goal=VID-G6 \
  --mount "type=bind,src=$docker_mod,dst=/go/pkg/mod,readonly" \
  --mount "type=volume,src=$cache_volume,dst=/root/.cache/go-build" -w /server \
  -e CGO_ENABLED=1 -e GOPROXY=off "${mysql_test_env[@]}" \
  "$go_image" bash -c '
    set -euo pipefail
    g6_copy_hash="$(find /server /docs /infra /scripts /tests /README.md -path /docs/evidence -prune -o -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum | sha256sum)"
    printf "VIDEO_G6_COPY_TREE_SHA256=%s\n" "${g6_copy_hash%% *}"
    # 全量service包包含五百余个测试并共享进程内合成ID序列，不能跨进程分片后复用同一数据库。
    # 40分钟仅是包级验收总预算；请求、事务、租约和故障屏障的业务超时保持原值。
    exec go test -race -count=1 -v -p 1 -timeout 40m "$@"
  ' g6-tests "${test_packages[@]}" -run "$test_filter")"
git -C "$repo_root" ls-files -z --cached --others --exclude-standard -- server docs infra scripts tests README.md \
  | tar -C "$repo_root" --null --no-recursion -T - -cf - \
  | MSYS_NO_PATHCONV=1 docker cp - "$build_id:/"
echo 'VIDEO_G6_SOURCE=CONTAINER_COPY_READY'
# SQL与Go取自同一容器快照，不能先应用工作区SQL再复制另一时刻的源码。
snapshot_sql() {
  MSYS_NO_PATHCONV=1 docker cp "$build_id:/server/migrations/$1" - | tar -xOf - | mysql_exec >/dev/null
}
latest=0
for path in "${migration_paths[@]}"; do
  base="$(basename "$path")"
  version="${base%%_*}"
  [[ "$version" =~ ^[0-9]{6}$ ]] || { echo 'VIDEO_G6_MYSQL=FAILED reason=migration_name'; exit 2; }
  if [[ "$focus" == legacy-model-publication ]] && ((10#$version > 77)); then break; fi
  if [[ "$focus" == save-migration ]] && ((10#$version >= 90)); then break; fi
  latest=$((10#$version))
  snapshot_sql "$base"
done
echo "VIDEO_G6_SCHEMA=PASS latest=$latest"
if [[ "$focus" == legacy-model-publication ]]; then
  [[ "$latest" == 77 ]] || { echo 'VIDEO_G6_MYSQL=FAILED reason=legacy_schema'; exit 2; }
  snapshot_sql 000077_video_billing_outbox_reconcile.up.sql
  snapshot_sql 000077_video_billing_outbox_reconcile.down.sql
  snapshot_sql 000077_video_billing_outbox_reconcile.up.sql
  MSYS_NO_PATHCONV=1 docker cp "$build_id:/infra/scripts/fixtures/video-g5-legacy-chat-g5-admin.sql" - | tar -xOf - | mysql_exec >/dev/null
else
[[ "$latest" -ge 78 ]] || { echo 'VIDEO_G6_MYSQL=FAILED reason=missing_g6_migration'; exit 2; }
snapshot_sql 000078_video_http_access_contract.up.sql
snapshot_sql 000078_video_http_access_contract.down.sql
snapshot_sql 000078_video_http_access_contract.up.sql
snapshot_sql 000079_video_rights_contract.up.sql
snapshot_sql 000079_video_rights_contract.down.sql
snapshot_sql 000079_video_rights_contract.up.sql
snapshot_sql 000080_video_rights_declarations.up.sql
snapshot_sql 000080_video_rights_declarations.down.sql
snapshot_sql 000080_video_rights_declarations.up.sql
snapshot_sql 000081_video_upload_controls.up.sql
snapshot_sql 000082_video_input_import_controls.up.sql
snapshot_sql 000082_video_input_import_controls.down.sql
snapshot_sql 000082_video_input_import_controls.up.sql
snapshot_sql 000081_video_upload_controls.down.sql
snapshot_sql 000081_video_upload_controls.up.sql
snapshot_sql 000083_video_input_deletion_requests.up.sql
snapshot_sql 000083_video_input_deletion_requests.down.sql
snapshot_sql 000083_video_input_deletion_requests.up.sql
snapshot_sql 000084_video_input_cleanup_facts.up.sql
snapshot_sql 000084_video_input_cleanup_facts.down.sql
snapshot_sql 000084_video_input_cleanup_facts.up.sql
snapshot_sql 000085_video_download_leases.up.sql
snapshot_sql 000085_video_download_leases.down.sql
snapshot_sql 000085_video_download_leases.up.sql
snapshot_sql 000086_video_cancellation_commands.up.sql
snapshot_sql 000086_video_cancellation_commands.down.sql
snapshot_sql 000086_video_cancellation_commands.up.sql
snapshot_sql 000087_video_media_deletion.up.sql
snapshot_sql 000087_video_media_deletion.down.sql
snapshot_sql 000087_video_media_deletion.up.sql
snapshot_sql 000088_video_asset_save_coordination.up.sql
snapshot_sql 000088_video_asset_save_coordination.down.sql
snapshot_sql 000088_video_asset_save_coordination.up.sql
snapshot_sql 000089_video_asset_save_cleanup.up.sql
snapshot_sql 000089_video_asset_save_cleanup.down.sql
snapshot_sql 000089_video_asset_save_cleanup.up.sql
if [[ "$focus" == save-migration ]]; then
  echo 'VIDEO_G6_MIGRATION_HISTORY=BASE89_GO_TEST_APPLIES90_91'
else
  snapshot_sql 000090_video_saved_entitlement_type.up.sql
  snapshot_sql 000090_video_saved_entitlement_type.down.sql
  snapshot_sql 000090_video_saved_entitlement_type.up.sql
  snapshot_sql 000091_video_asset_save_attempts.up.sql
  snapshot_sql 000091_video_asset_save_attempts.down.sql
  snapshot_sql 000091_video_asset_save_attempts.up.sql
  snapshot_sql 000092_video_asset_deletion.up.sql
  snapshot_sql 000092_video_asset_deletion.down.sql
  snapshot_sql 000092_video_asset_deletion.up.sql
  snapshot_sql 000093_video_callback_nonce.up.sql
  snapshot_sql 000093_video_callback_nonce.down.sql
  snapshot_sql 000093_video_callback_nonce.up.sql
  snapshot_sql 000094_video_admin_cancellation.up.sql
  snapshot_sql 000094_video_admin_cancellation.down.sql
  snapshot_sql 000094_video_admin_cancellation.up.sql
  snapshot_sql 000095_video_admin_input_quarantine.up.sql
  snapshot_sql 000095_video_admin_input_quarantine.down.sql
  snapshot_sql 000095_video_admin_input_quarantine.up.sql
  snapshot_sql 000096_video_admin_output_quarantine.up.sql
  snapshot_sql 000096_video_admin_output_quarantine.down.sql
  snapshot_sql 000096_video_admin_output_quarantine.up.sql
  snapshot_sql 000097_video_output_release_approval.up.sql
  snapshot_sql 000097_video_output_release_approval.down.sql
  snapshot_sql 000097_video_output_release_approval.up.sql
  snapshot_sql 000098_video_admin_poll_commands.up.sql
  snapshot_sql 000098_video_admin_poll_commands.down.sql
  snapshot_sql 000098_video_admin_poll_commands.up.sql
  snapshot_sql 000099_video_archive_task_fence.up.sql
  snapshot_sql 000099_video_archive_task_fence.down.sql
  snapshot_sql 000099_video_archive_task_fence.up.sql
  snapshot_sql 000100_video_admin_archive_commands.up.sql
  snapshot_sql 000100_video_admin_archive_commands.down.sql
  snapshot_sql 000100_video_admin_archive_commands.up.sql
  snapshot_sql 000101_video_adjustment_approvals.up.sql
  snapshot_sql 000101_video_adjustment_approvals.down.sql
  snapshot_sql 000101_video_adjustment_approvals.up.sql
  snapshot_sql 000102_video_model_contract_draft.up.sql
  snapshot_sql 000102_video_model_contract_draft.down.sql
  snapshot_sql 000102_video_model_contract_draft.up.sql
  snapshot_sql 000103_video_model_draft_commands.up.sql
  snapshot_sql 000103_video_model_draft_commands.down.sql
  snapshot_sql 000103_video_model_draft_commands.up.sql
  snapshot_sql 000104_video_model_draft_adoption.up.sql
  snapshot_sql 000104_video_model_draft_adoption.down.sql
  snapshot_sql 000104_video_model_draft_adoption.up.sql
  snapshot_sql 000105_video_model_publication_commands.up.sql
  snapshot_sql 000105_video_model_publication_commands.down.sql
  snapshot_sql 000105_video_model_publication_commands.up.sql
  snapshot_sql 000106_video_project_grant_commands.up.sql
  snapshot_sql 000106_video_project_grant_commands.down.sql
  snapshot_sql 000106_video_project_grant_commands.up.sql
  snapshot_sql 000107_video_project_key_commands.up.sql
  snapshot_sql 000107_video_project_key_commands.down.sql
  snapshot_sql 000107_video_project_key_commands.up.sql
  snapshot_sql 000108_video_inline_upload_controls.up.sql
  snapshot_sql 000108_video_inline_upload_controls.down.sql
  snapshot_sql 000108_video_inline_upload_controls.up.sql
  snapshot_sql 000109_video_queue_admission_guard.up.sql
  snapshot_sql 000109_video_queue_admission_guard.down.sql
  snapshot_sql 000109_video_queue_admission_guard.up.sql
fi
fi
docker start -a "$build_id" | awk -v "required=$required_tests" -f "$repo_root/infra/scripts/verify-video-g5-test-execution.awk"
test_exit="$(docker inspect --format '{{.State.ExitCode}}' "$build_id")"
[[ "$test_exit" == 0 ]] || { echo 'VIDEO_G6_MYSQL=FAILED reason=tests'; exit 2; }
if [[ "$focus" == admin-adjustments ]]; then
  # G5测试保持专用库名守卫，用另一套本Goal临时容器验证，不能以SKIP或放宽库名替代兼容回归。
  VIDEO_GATEWAY_G6_TEST_FOCUS=legacy-adjustments bash "$repo_root/infra/scripts/verify-video-gateway-vid-g6.sh"
  echo 'VIDEO_G6_ADJUSTMENT_COMPATIBILITY=PASS isolated_g5_required=true'
fi
if [[ "$focus" == model-contract ]]; then
  # 当前目录/合同与旧Chat发布分别在102和77号独立临时库实际通过后，才结束组合专项。
  VIDEO_GATEWAY_G6_TEST_FOCUS=legacy-model-publication bash "$repo_root/infra/scripts/verify-video-gateway-vid-g6.sh"
  echo 'VIDEO_G6_MODEL_CONTRACT_COMPATIBILITY=PASS legacy_schema=77 current_schema_min=102'
fi
if [[ "$focus" == model-publication ]]; then
  # 视频native发布与旧Chat/Bifrost发布使用不同schema边界，两者必须在同一源码的独立临时库实际通过。
  VIDEO_GATEWAY_G6_TEST_FOCUS=legacy-model-publication bash "$repo_root/infra/scripts/verify-video-gateway-vid-g6.sh"
  echo 'VIDEO_G6_MODEL_PUBLICATION_COMPATIBILITY=PASS current_schema_min=105 legacy_schema=77'
fi
echo "VIDEO_G6_MYSQL=PASS scope=$focus full_stage=false real_provider_calls=0 real_wallet_writes=0 cost_cny=0 test_server_writes=0 production_operations=0"
