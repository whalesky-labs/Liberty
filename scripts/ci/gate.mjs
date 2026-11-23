import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { fullSuites, planVersion } from './plan.mjs';

export function assertGate(plan, results, head) {
  assert.equal(results.plan, 'success', 'CI selection did not succeed');
  assert.equal(plan?.format, planVersion, 'Unknown CI plan');
  assert.match(head ?? '', /^[a-f0-9]{40}$/);
  assert.equal(plan.head, head, 'CI plan belongs to another commit');
  assert.equal(typeof plan.full, 'boolean');
  assert.ok(Array.isArray(plan.suites) && plan.suites.length > 0 && new Set(plan.suites).size === plan.suites.length);
  assert.ok(plan.suites.every(suite => [...fullSuites, 'version'].includes(suite)), 'Unknown required suite');
  if (plan.full) {
    assert.deepEqual(plan.suites, fullSuites, 'Incomplete full-validation plan');
    assert.equal(results.full, 'success', 'Required full validation failed, was cancelled or skipped');
    assert.equal(results.checks, 'skipped', 'Unexpected scoped validation');
  } else {
    assert.equal(results.checks, 'success', 'Required scoped validation failed, was cancelled or skipped');
    assert.equal(results.full, 'skipped', 'Unexpected full validation');
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    assertGate(JSON.parse(process.env.CI_PLAN ?? 'null'), JSON.parse(process.env.CI_RESULTS ?? '{}'), process.env.GITHUB_SHA);
    console.log('Every check required by this commit passed.');
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
