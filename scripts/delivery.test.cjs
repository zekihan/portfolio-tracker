const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const { mkdtempSync, readFileSync, rmSync, writeFileSync } = require('node:fs');
const os = require('node:os');
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

const settingsPage = '<!DOCTYPE html><html><div class="settings-page"><h1>Settings</h1></div></html>';
const offlineDashboard = '<!DOCTYPE html><html><div class="dashboard"><section role="alert">' +
  '<h1>Portfolio valuation unavailable</h1><p>Values and allocations are unavailable.</p>' +
  '<a href="/?currency=TRY">Retry</a></section></div></html>';
function htmlResponse(status, body) {
  return `HTTP/1.0 ${status}\r\nContent-Type: text/html; charset=utf-8\r\n\r\n${body}`;
}

function containerSmoke(t, responses = {}) {
  const directory = mkdtempSync(path.join(os.tmpdir(), 'portfolio-smoke-test-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  writeFileSync(path.join(directory, 'settings'), responses.settings ?? htmlResponse('200 OK', settingsPage));
  writeFileSync(path.join(directory, 'dashboard'), responses.dashboard ?? htmlResponse('503 Service Unavailable', offlineDashboard));
  writeFileSync(path.join(directory, 'probe.cjs'), String.raw`
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const args = process.argv.slice(2);
assert.match(args[0], /exec 9>&1/);
assert.match(args[0], /exec timeout 15 nc -w 10 127\.0\.0\.1 8080 -e sh -c "\$@"/);
assert.equal(args[1], 'sh');
assert.equal(args[2], 'printf "\$@"; exec cat >&9');
assert.equal(args[3], 'sh');
const rendered = spawnSync('sh', ['-c', 'printf "\$@"', 'sh', ...args.slice(4)], { encoding: 'utf8' });
assert.equal(rendered.status, 0, rendered.stderr);
const request = rendered.stdout;
fs.appendFileSync(path.join(process.env.MOCK_DIR, 'calls'), 'command-mode probe: ' + request.split('\r\n')[0] + '\n');
if (request.startsWith('GET /settings HTTP/1.0')) {
  process.stdout.write(fs.readFileSync(path.join(process.env.MOCK_DIR, 'settings')));
} else if (request.startsWith('GET / HTTP/1.0')) {
  process.stdout.write(fs.readFileSync(path.join(process.env.MOCK_DIR, 'dashboard')));
} else if (request.startsWith('PATCH /api/settings/currency HTTP/1.1')) {
  assert.equal(request.split('\r\n\r\n')[1], '{"currency":"USD"}');
  fs.writeFileSync(path.join(process.env.MOCK_DIR, 'updated'), '');
  process.stdout.write('HTTP/1.1 200 OK\r\n\r\n');
} else {
  throw new Error('Unexpected request: ' + request);
}
`);
  writeFileSync(path.join(directory, 'docker'), `#!/bin/sh
set -eu
printf '%s\\n' "$*" >> "$MOCK_DIR/calls"
case "$*" in
  'build --tag '*) ;;
  'image inspect --format {{.Config.User}} '*) printf '25110:25110\\n' ;;
  'run --detach --network none '*) printf 'smoke-container\\n' ;;
  'run --rm --network container:smoke-container '*'/api/settings')
    if test -f "$MOCK_DIR/updated"; then
      printf '{"display_currency":"USD"}\\n'
    else
      printf '{"display_currency":"TRY"}\\n'
    fi ;;
  'run --rm --network container:smoke-container '*'/static/style.css' | \\
  'run --rm --network container:smoke-container '*'/static/app.js') printf 'asset\\n' ;;
  'run --rm --network container:smoke-container '*' sh -c '*)
    shift 7
    exec node "$MOCK_DIR/probe.cjs" "$@" ;;
  'cp smoke-container:/data/portfolio.db '* | \\
  'cp smoke-container:/usr/share/licenses/portfolio-tracker/LICENSE '*) printf 'fixture\\n' > "$3" ;;
  'restart smoke-container') printf 'smoke-container\\n' ;;
  'logs smoke-container' | 'rm --force --volumes smoke-container') ;;
  *) echo "Unexpected Docker command: $*" >&2; exit 1 ;;
esac
`, { mode: 0o755 });
  const result = spawnSync('sh', [path.join(__dirname, 'container_smoke.sh')], {
    cwd: root,
    env: { ...process.env, PATH: `${directory}${path.delimiter}${process.env.PATH}`, MOCK_DIR: directory },
    encoding: 'utf8',
    timeout: 10000,
  });
  const calls = readFileSync(path.join(directory, 'calls'), 'utf8');
  assert.match(calls, /run --detach --network none /, 'application must remain network-isolated');
  assert.match(calls, /rm --force --volumes smoke-container/, 'container must be cleaned up');
  return { ...result, calls };
}

test('container smoke accepts only the intended offline pages and still checks persistence', t => {
  const result = containerSmoke(t);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /Container smoke test passed/);
  assert.match(result.calls, /restart smoke-container/);
  assert.equal((result.calls.match(/command-mode probe:/g) || []).length, 3, 'all GET and PATCH probes must use command mode');
  assert.doesNotMatch(result.calls, /--interactive/, 'HTTP probes must not half-close finite Docker stdin');
  assert.match(result.calls, /cp smoke-container:\/data\/portfolio.db /);
  assert.match(result.calls, /cp smoke-container:\/usr\/share\/licenses\/portfolio-tracker\/LICENSE /);
});

test('container smoke rejects unavailable, incomplete, or misleading page responses', async t => {
  for (const [name, responses] of [
    ['settings failure', { settings: htmlResponse('503 Service Unavailable', settingsPage) }],
    ['settings wrong page', { settings: htmlResponse('200 OK', offlineDashboard) }],
    ['settings truncated', { settings: htmlResponse('200 OK', settingsPage.replace('</html>', '')) }],
    ['generic dashboard 503', { dashboard: htmlResponse('503 Service Unavailable', 'Service Unavailable') }],
    ['wrong dashboard status', { dashboard: htmlResponse('500 Internal Server Error', offlineDashboard) }],
    ['dashboard falsely succeeds', { dashboard: htmlResponse('200 OK', offlineDashboard) }],
    ['dashboard not HTML', { dashboard: htmlResponse('503 Service Unavailable', offlineDashboard).replace('text/html', 'text/plain') }],
    ['dashboard truncated', { dashboard: htmlResponse('503 Service Unavailable', offlineDashboard.replace('</html>', '')) }],
    ['dashboard missing retry', { dashboard: htmlResponse('503 Service Unavailable', offlineDashboard.replace('>Retry</a>', '>Back</a>')) }],
    ['dashboard misleading values', { dashboard: htmlResponse('503 Service Unavailable', offlineDashboard.replace('</div>', '<div id="current-value-try">0.00</div></div>')) }],
  ]) {
    await t.test(name, t => {
      const result = containerSmoke(t, responses);
      assert.notEqual(result.status, 0, 'smoke accepted an invalid page');
      assert.doesNotMatch(result.stdout, /Container smoke test passed/);
      assert.doesNotMatch(result.calls, /restart smoke-container/, 'must stop before persistence checks');
    });
  }
});
