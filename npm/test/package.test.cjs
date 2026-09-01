'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { genericConfiguration, parseInstallArgs, writeJsonClient } = require('../lib/install.cjs');
const { parseChecksums, resolveAsset, sha256 } = require('../lib/native.cjs');

test('maps supported npm platforms to release assets', () => {
  assert.equal(resolveAsset('linux', 'x64'), 'code-relay-agent-linux-amd64');
  assert.equal(resolveAsset('darwin', 'arm64'), 'code-relay-agent-darwin-arm64');
  assert.equal(resolveAsset('win32', 'x64'), 'code-relay-agent-windows-amd64.exe');
  assert.throws(() => resolveAsset('win32', 'arm64'), /Unsupported platform/);
});

test('parses GNU checksum files and hashes bytes', () => {
  const digest = sha256(Buffer.from('relay'));
  const parsed = parseChecksums(`${digest}  code-relay-agent-linux-amd64\n`);
  assert.equal(parsed.get('code-relay-agent-linux-amd64'), digest);
});

test('requires an explicit supported install client', () => {
  assert.throws(() => parseInstallArgs([]), /--client/);
  assert.equal(parseInstallArgs(['--client', 'codex', '--yes']).yes, true);
  assert.throws(() => parseInstallArgs(['--client', 'unknown']), /--client/);
});

test('merges project MCP JSON without deleting existing servers', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'code-relay-npm-'));
  const target = path.join(root, '.mcp.json');
  fs.writeFileSync(target, JSON.stringify({ mcpServers: { existing: { command: 'existing' } } }));
  const result = writeJsonClient('claude-code', root, '3.1.0', { yes: true });
  assert.equal(result.changed, true);
  const document = JSON.parse(fs.readFileSync(target, 'utf8'));
  assert.equal(document.mcpServers.existing.command, 'existing');
  assert.deepEqual(document.mcpServers['code-relay'].args, ['-y', 'code-relay-mcp@3.1.0']);
  assert.equal(writeJsonClient('claude-code', root, '3.1.0', { yes: true }).changed, false);
});

test('generic configuration is deterministic and pinned', () => {
  assert.deepEqual(genericConfiguration('3.1.0'), {
    mcpServers: { 'code-relay': { command: 'npx', args: ['-y', 'code-relay-mcp@3.1.0'] } }
  });
});
