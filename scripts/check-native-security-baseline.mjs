import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { assertCandidateManifest, candidateIdentity } from "./lib/native-package.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const native = path.join(root, "apps/native");
const execute = promisify(execFile);

// Explicit names prevent a renamed/deleted/skipped regression from silently
// turning a successful go test exit into a security acceptance result.
export const nativeSecurityTests = Object.freeze({
  "./internal/application": [
    "TestWindowCapabilities", "TestWindowCapabilitySnapshotIsSourceBoundAndRevocable",
    "TestWindowPermissionDelegationMatrix", "TestWindowMismatchedSnapshotsCannotMutateOrExport",
    "TestResourceEditorScopesRejectForeignDraftsWithoutDatabaseOrCredentialEffects",
    "TestCredentialPublicationRejectsUnverifiedWritesAndRecoversAfterReopen",
  ],
  "./internal/infrastructure/profile": [
    "TestLockExcludesAnotherProcessAndSurvivesReopen", "TestLockRejectsSymlinkWithoutModifyingTarget",
    "TestProfileLocationsKeepCandidateSeparate", "TestCandidateIsolationRejectsProductionAliasesAndDatabaseLinksBeforeCreation",
  ],
  "./internal/infrastructure/profiletransfer": [
    "TestImportConsistentWALSnapshotSeparatesActiveDataAndCredentialOwnership",
    "TestImportAmbiguousVaultFailureRetainsIntentAndRetriesWithoutPublishing",
    "TestImportReadyUnpublishedCompensationFailureCanRetryWithoutTouchingSourceReferences",
    "TestImportRejectsExistingForeignLinkedAndOverlappingTargets",
    "TestImportRejectsActiveLegacyOwnershipBeforeCredentialWritesAndCanRetry",
    "TestImportAllowsIdleAndFailedRuntimeOperationsWithoutChangingSource",
    "TestImportRejectsEveryActiveAndUnknownRuntimeOperationBeforeCredentialWrites",
    "TestImportedProfileRestoreRetainsNativeChangesWithoutTouchingLegacy",
  ],
  "./internal/infrastructure/repositories/schema": [
    "TestTransferredEarlySchemaUsesExecutorWithoutDeletingSourceCredentialAccounts",
  ],
  "./cmd/liberty-native": [
    "TestProductionProfileIsReadOnlyAndRequiresIsolatedImportForMutation",
    "TestImportOptionsRequireOfflineIsolatedDirectoryWithoutChangingReadOnlyPreview",
    "TestOfflineImportCommandIsIdempotentAndDoesNotDiscloseProfilePaths",
    "TestStartupFailureProvidesSafeRecoveryInstructions",
  ],
  "./internal/infrastructure/managed": [
    "TestArchivePreflightRejectsEscapesAndAliasesBeforeWrites",
    "TestArchiveRejectsEscapingLinksParentsAndTruncatedGzip", "TestArchiveCancellationLeavesNoPartialOutput",
    "TestDownloadRejectsInsecureSourceAndSymlinkTarget", "TestManifestRejectsMutableMissingAndInvalidAssets",
    "TestProductionVendorInstallationUsesVerifiedOutputsAndIsolatedPip",
    "TestProductionVendorInstallationRejectsPartialOrTamperedOutputAndRetries", "TestProductionVendorCommandSnapshot",
    "TestProductionVendorRejectsOversizedUpstreamBeforeBuild",
  ],
  "./internal/infrastructure/remote": ["TestRemoteTargetDNSAndEndpointPolicy"],
  "./internal/infrastructure/ai": ["TestDestinationPolicyAndPinnedDNS"],
  "./internal/infrastructure/runner": [
    "TestInvalidV2Results", "TestV1ResultCaseAliasesCannotChangeOutcomeOrTranscript",
    "TestProgressAndEventValidation", "TestProgressFencePerAttempt", "TestAttemptPathsAndIdempotentTrash",
  ],
  "./internal/infrastructure/diagnostics": [
    "TestSensitiveValuesAreRemovedCompletely", "TestIncompleteSensitiveValuesFailClosed",
    "TestLocationsWithSpacesAndUNCDoNotLeakSuffixes",
  ],
});

export function assertSecurityTestResults(events, required) {
  for (const name of required) {
    const endings = events.filter((event) => event.Test === name && ["pass", "skip", "fail"].includes(event.Action));
    assert.ok(endings.length === 1 && endings[0].Action === "pass", `Required Native security regression did not pass: ${name}`);
  }
  assert.ok(!events.some((event) => event.Action === "fail"), "Native security test package failed");
}

// The credential adapter may use only the platform vault and pure conversion
// helpers. New direct dependencies require review instead of permitting file,
// SQLite or network persistence as an unnoticed fallback.
export function assertCredentialImports(imports) {
  const allowed = new Set([
    "context", "encoding/hex", "errors", "os/exec", "runtime", "strings", "time", "unicode/utf8", "unsafe",
    "golang.org/x/sys/windows", "liberty.local/liberty/internal/domain",
  ]);
  assert.ok(Array.isArray(imports) && imports.length, "Missing system credential adapter imports");
  for (const value of imports) assert.ok(allowed.has(value), `System credential dependency requires security review: ${value}`);
}

export function assertNativeProductionBlock() {
  const file = { path: "sample", bytes: 1, sha256: "a".repeat(64) };
  const manifest = { format: 1, channel: "candidate", identity: candidateIdentity, version: "1.0.0", target: "windows-x64", productionReady: false, signed: false, notarized: false,
    files: ["sample", "LICENSE", "README.txt", "NOTICES.txt"].map((name) => ({ ...file, path: name })) };
  assertCandidateManifest(manifest);
  for (const field of ["productionReady", "signed", "notarized"]) {
    assert.throws(() => assertCandidateManifest({ ...manifest, [field]: true }), `Candidate must not assert ${field}`);
  }
}

export async function checkNativeSecurityBaseline() {
  const env = { ...process.env, GOTOOLCHAIN: "go1.27.4", GOFLAGS: "-mod=readonly", GOWORK: "off" };
  // Subprocess fixture modes must not affect the selected regression process.
  for (const name of Object.keys(env)) if (name.startsWith("LIBERTY_")) delete env[name];
  assertNativeProductionBlock();
  for (const [goos, goarch] of [["darwin", "arm64"], ["darwin", "amd64"], ["windows", "amd64"], ["linux", "amd64"]]) {
    const { stdout } = await execute("go", ["list", "-json", "./internal/infrastructure/credentials"], { cwd: native, env: { ...env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: "0" }, maxBuffer: 4 * 1024 * 1024 });
    assertCredentialImports(JSON.parse(stdout).Imports);
  }
  let count = 0;
  for (const [pkg, names] of Object.entries(nativeSecurityTests)) {
    const pattern = `^(${names.join("|")})$`;
    const { stdout } = await execute("go", ["test", "-json", pkg, "-run", pattern, "-count=1", "-timeout=2m"], { cwd: native, env, maxBuffer: 16 * 1024 * 1024, timeout: 150_000 });
    const events = stdout.trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
    assertSecurityTestResults(events, names);
    count += names.length;
    console.log(`Native security regressions passed: ${pkg} (${names.length})`);
  }
  console.log(`Native security baseline passed ${count} required regressions and credential dependency policy. This is not a vulnerability scan, system-vault acceptance, target-device test or production release clearance.`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  assert.equal(process.argv.length, 2, "Native security baseline accepts no skip flags");
  await checkNativeSecurityBaseline();
}
