'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

function parseInstallArgs(args) {
  const options = { client: '', root: process.cwd(), yes: false, force: false, dryRun: false };
  for (let index = 0; index < args.length; index += 1) {
    const value = args[index];
    if (value === '--client') options.client = args[++index] || '';
    else if (value === '--root') options.root = path.resolve(args[++index] || '.');
    else if (value === '--yes' || value === '-y') options.yes = true;
    else if (value === '--force') options.force = true;
    else if (value === '--dry-run') options.dryRun = true;
    else throw new Error(`Unknown install option: ${value}`);
  }
  if (!['codex', 'claude-code', 'cursor', 'vscode', 'generic'].includes(options.client)) {
    throw new Error('Use --client codex|claude-code|cursor|vscode|generic');
  }
  return options;
}

function packageCommand(version, vscode = false) {
  const entry = { command: 'npx', args: ['-y', `code-relay-mcp@${version}`] };
  if (vscode) entry.type = 'stdio';
  return entry;
}

function clientTarget(client, root) {
  if (client === 'claude-code') return { file: path.join(root, '.mcp.json'), key: 'mcpServers' };
  if (client === 'cursor') return { file: path.join(root, '.cursor', 'mcp.json'), key: 'mcpServers' };
  if (client === 'vscode') return { file: path.join(root, '.vscode', 'mcp.json'), key: 'servers' };
  return null;
}

function writeJsonClient(client, root, version, options = {}) {
  const target = clientTarget(client, root);
  if (!target) throw new Error(`No JSON configuration target for ${client}`);
  let document = {};
  let existed = false;
  if (fs.existsSync(target.file)) {
    existed = true;
    try {
      document = JSON.parse(fs.readFileSync(target.file, 'utf8'));
    } catch (error) {
      throw new Error(`Refusing to overwrite invalid JSON in ${target.file}: ${error.message}`);
    }
  }
  if (!document || Array.isArray(document) || typeof document !== 'object') {
    throw new Error(`Refusing to overwrite non-object JSON in ${target.file}`);
  }
  const servers = document[target.key] || {};
  if (!servers || Array.isArray(servers) || typeof servers !== 'object') {
    throw new Error(`${target.key} must be an object in ${target.file}`);
  }
  if (servers['code-relay'] && !options.force) {
    return { changed: false, file: target.file, message: 'Code Relay is already configured; use --force to replace it.' };
  }
  servers['code-relay'] = packageCommand(version, client === 'vscode');
  document[target.key] = servers;
  if (options.dryRun || !options.yes) {
    return { changed: false, file: target.file, preview: document, message: 'Preview only; rerun with --yes to write this configuration.' };
  }
  fs.mkdirSync(path.dirname(target.file), { recursive: true });
  if (existed) {
    const backup = `${target.file}.code-relay.${Date.now()}.bak`;
    fs.copyFileSync(target.file, backup);
  }
  const temporary = `${target.file}.${process.pid}.tmp`;
  fs.writeFileSync(temporary, `${JSON.stringify(document, null, 2)}\n`, { mode: 0o600 });
  if (process.platform === 'win32' && existed) fs.rmSync(target.file, { force: true });
  fs.renameSync(temporary, target.file);
  return { changed: true, file: target.file, message: 'Code Relay MCP configuration installed.' };
}

function installCodex(version, options = {}) {
  const command = process.platform === 'win32' ? 'codex.exe' : 'codex';
  const current = spawnSync(command, ['mcp', 'get', 'code-relay'], { encoding: 'utf8', shell: false });
  if (current.status === 0 && !options.force) {
    return { changed: false, message: 'Code Relay is already configured in Codex; use --force to replace it.' };
  }
  const addArgs = ['mcp', 'add', 'code-relay', '--', 'npx', '-y', `code-relay-mcp@${version}`];
  if (options.dryRun || !options.yes) {
    return { changed: false, command: [command, ...addArgs], message: 'Preview only; rerun with --yes to modify Codex configuration.' };
  }
  if (current.status === 0) {
    const removed = spawnSync(command, ['mcp', 'remove', 'code-relay'], { encoding: 'utf8', shell: false });
    if (removed.status !== 0) throw new Error(removed.stderr || 'Unable to replace existing Codex MCP configuration');
  }
  const added = spawnSync(command, addArgs, { encoding: 'utf8', shell: false });
  if (added.error) throw added.error;
  if (added.status !== 0) throw new Error(added.stderr || 'Unable to add Code Relay to Codex');
  return { changed: true, message: 'Code Relay MCP configuration installed in Codex.' };
}

function genericConfiguration(version) {
  return { mcpServers: { 'code-relay': packageCommand(version) } };
}

function install(args, version) {
  const options = parseInstallArgs(args);
  if (options.client === 'generic') {
    return { changed: false, preview: genericConfiguration(version), message: 'Merge this entry into the client MCP configuration.' };
  }
  if (options.client === 'codex') return installCodex(version, options);
  return writeJsonClient(options.client, options.root, version, options);
}

module.exports = { clientTarget, genericConfiguration, install, packageCommand, parseInstallArgs, writeJsonClient };
