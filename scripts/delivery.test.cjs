const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const { readFileSync } = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const root = path.resolve(__dirname, '..');
function shell(script, ...args) {
  return spawnSync('sh', [path.join(__dirname, script), ...args], { cwd: root, encoding: 'utf8' });
}

test('release versions are strict and increasing without numeric precision loss', () => {
  for (const version of ['', '1.2', '01.2.3', '1.02.3', '1.2.03', '1.2.3-beta', '1.2.3\n2.3.4', 'v1.2.3']) {
    assert.notEqual(shell('validate_version.sh', version).status, 0, version);
  }
  for (const [next, current] of [['1.2.3', '1.2.3'], ['1.2.2', '1.2.3'], ['0.9.9', '1.0.0'], ['2.0.0', 'bad']]) {
    assert.notEqual(shell('validate_version.sh', next, current).status, 0, `${next} > ${current}`);
  }
  for (const [next, current] of [['1.2.4', '1.2.3'], ['1.3.0', '1.2.9'], ['2.0.0', '1.9.9'], ['1.0.9007199254740993', '1.0.9007199254740992']]) {
    const result = shell('validate_version.sh', next, current);
    assert.equal(result.status, 0, result.stderr);
  }
});

test('release tags must exactly match VERSION', () => {
  const version = readFileSync(path.join(root, 'VERSION'), 'utf8').trim();
  assert.equal(shell('check_release_tag.sh', `v${version}`).status, 0);
  for (const tag of [version, 'v0.0.0', 'main', `v${version}-rc1`, '']) {
    assert.notEqual(shell('check_release_tag.sh', tag).status, 0, tag);
  }
});

test('development container publication never overrides semantic-version tags', () => {
  const workflow = readFileSync(path.join(root, '.github/workflows/docker_release.yaml'), 'utf8');
  assert.match(workflow, /tags: \['v\*\.\*\.\*'\]/);
  assert.match(workflow, /check_release_tag\.sh/);
  assert.match(workflow, /type=sha,format=long/);
  assert.doesNotMatch(workflow, /value=\$\{\{ steps\.version/);
  for (const line of workflow.split('\n').filter(line => line.includes('type=semver'))) {
    assert.ok(!line.includes('value='), 'semver tags must derive only from Git tags');
  }
});

test('native platform tests cover release operating systems without duplicating builds', () => {
  const workflow = readFileSync(path.join(root, '.github/workflows/pr.yaml'), 'utf8');
  const native = workflow.slice(workflow.indexOf('  native-tests:'));
  assert.match(native, /macos-latest, windows-latest/);
  assert.match(native, /go test -count=1 -timeout=5m/);
  assert.doesNotMatch(native, /goreleaser|make lint/);
});

test('make run uses Go architecture names and a writable local database default', () => {
  for (const [os, arch, suffix] of [['linux', 'arm64', ''], ['darwin', 'arm64', ''], ['windows', 'amd64', '.exe']]) {
    const env = { ...process.env, GOOS: os, GOARCH: arch };
    delete env.DB_PATH;
    const result = spawnSync('make', ['-n', 'run'], { cwd: root, env, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    assert.ok(result.stdout.includes(`DB_PATH="./portfolio.db" ./dist/portfolio-tracker_${os}_${arch}/portfolio-tracker${suffix}`));
  }
  const result = spawnSync('make', ['-n', 'run', 'DB_PATH=/tmp/custom portfolio.db'], { cwd: root, encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /DB_PATH="\/tmp\/custom portfolio\.db"/);
});
