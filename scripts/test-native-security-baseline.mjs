import assert from "node:assert/strict";
import test from "node:test";
import { assertCredentialImports, assertNativeProductionBlock, assertSecurityTestResults, nativeSecurityTests } from "./check-native-security-baseline.mjs";

test("security acceptance requires each selected Go regression to pass exactly once", () => {
  const required = ["TestWindowCapabilities", "TestLockExcludesAnotherProcessAndSurvivesReopen"];
  const events = required.map((Test) => ({ Action: "pass", Test }));
  assert.doesNotThrow(() => assertSecurityTestResults(events, required));
  for (const changed of [
    [], events.slice(1), [{ ...events[0], Action: "skip" }, events[1]],
    [{ ...events[0], Action: "fail" }, events[1]], [...events, events[0]],
    [...events, { Action: "fail", Package: "package" }],
  ]) assert.throws(() => assertSecurityTestResults(changed, required));
});

test("credential dependencies refuse filesystem, SQL, network and unreviewed fallback imports", () => {
  assert.doesNotThrow(() => assertCredentialImports(["context", "os/exec", "liberty.local/liberty/internal/domain"]));
  assert.doesNotThrow(() => assertCredentialImports(["golang.org/x/sys/windows", "unsafe"]));
  for (const dependency of ["os", "path/filepath", "database/sql", "net/http", "encoding/json", "example.com/fallback"]) {
    assert.throws(() => assertCredentialImports(["context", dependency]), /requires security review/);
  }
  assert.throws(() => assertCredentialImports([]));
});

test("Native security gate keeps candidate release claims blocked", () => {
  assert.doesNotThrow(assertNativeProductionBlock);
});

test("security plan selects synthetic boundary tests without system credential or GUI acceptance", () => {
  for (const names of Object.values(nativeSecurityTests)) {
    assert.ok(names.length);
    assert.equal(new Set(names).size, names.length);
    assert.ok(names.every((name) => /^Test[A-Za-z0-9]+$/.test(name)));
    assert.ok(names.every((name) => !/IsolatedSystem|WindowAcceptance|PinnedRuntimeAcceptance/.test(name)));
  }
  const selected = Object.values(nativeSecurityTests).flat();
  for (const required of ["TestWindowCapabilities", "TestCredentialPublicationRejectsUnverifiedWritesAndRecoversAfterReopen", "TestLockExcludesAnotherProcessAndSurvivesReopen", "TestArchivePreflightRejectsEscapesAndAliasesBeforeWrites", "TestRemoteTargetDNSAndEndpointPolicy", "TestInvalidV2Results"]) {
    assert.ok(selected.includes(required), required);
  }
});
