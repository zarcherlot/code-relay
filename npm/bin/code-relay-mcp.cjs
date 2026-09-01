#!/usr/bin/env node
'use strict';

const { spawn } = require('node:child_process');
const packageJson = require('../../package.json');
const { install } = require('../lib/install.cjs');
const { ensureAgent } = require('../lib/native.cjs');

function usage() {
  process.stdout.write(`Code Relay MCP ${packageJson.version}\n\n` +
    'Usage:\n' +
    '  code-relay-mcp                         Start the MCP stdio server\n' +
    '  code-relay-mcp <agent-command> [...]   Run a native Code Relay command\n' +
    '  code-relay-mcp install --client <name> [--yes] [--force]\n' +
    '  code-relay-mcp version\n\n' +
    'Install clients: codex, claude-code, cursor, vscode, generic\n');
}

async function main() {
  const args = process.argv.slice(2);
  if (args[0] === '--help' || args[0] === '-h' || args[0] === 'help') {
    usage();
    return;
  }
  if (args[0] === '--version' || args[0] === '-v' || args[0] === 'version') {
    process.stdout.write(`code-relay-mcp ${packageJson.version}\n`);
    return;
  }
  if (args[0] === 'install') {
    const result = install(args.slice(1), packageJson.version);
    process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
    return;
  }

  const binary = await ensureAgent({ version: packageJson.version });
  const nativeArgs = args.length === 0 ? ['mcp-stdio'] : args;
  const child = spawn(binary, nativeArgs, { stdio: 'inherit', shell: false, windowsHide: true });
  for (const signal of ['SIGINT', 'SIGTERM']) {
    process.on(signal, () => {
      if (!child.killed) child.kill(signal);
    });
  }
  child.on('error', (error) => {
    process.stderr.write(`Code Relay failed to start: ${error.message}\n`);
    process.exitCode = 1;
  });
  child.on('exit', (code, signal) => {
    process.exitCode = code == null ? (signal ? 1 : 0) : code;
  });
}

main().catch((error) => {
  process.stderr.write(`Code Relay: ${error.message}\n`);
  process.exitCode = 1;
});
