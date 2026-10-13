const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');

test('repository policy overrides inherited Docker digest branch automerge', () => {
  const config = JSON.parse(readFileSync(path.join(__dirname, '..', 'renovate.json'), 'utf8'));
  assert.ok(config.extends.includes('github>zekihan/renovate-config'));

  // Presets resolve before repository packageRules; later matching rules win.
  // This fixture captures the shared automerge-docker-digest preset's unsafe defaults.
  const inherited = { automerge: true, automergeType: 'branch', ignoreTests: true };
  const rules = config.packageRules.filter(rule =>
    rule.matchDatasources?.includes('docker') && rule.matchUpdateTypes?.includes('digest'));
  assert.equal(rules.length, 1, 'keep one explicit repository Docker digest override');
  const rule = rules[0];
  assert.deepEqual(rule.matchDatasources, ['docker']);
  assert.deepEqual(rule.matchUpdateTypes, ['digest', 'pinDigest']);
  assert.deepEqual({ ...inherited, ...rule }, {
    ...rule,
    automerge: false,
    automergeType: 'pr',
    ignoreTests: false,
  });
  assert.equal(rule.prCreation, 'immediate', 'PR creation must trigger pull_request CI');
});
