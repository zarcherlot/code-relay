'use strict';

const fs = require('node:fs');
const path = require('node:path');

const root = path.resolve(__dirname, '..', '..');
const packageJson = require(path.join(root, 'package.json'));
const plugin = JSON.parse(fs.readFileSync(path.join(root, '.codex-plugin', 'plugin.json'), 'utf8'));
const server = JSON.parse(fs.readFileSync(path.join(root, 'server.json'), 'utf8'));
const pluginVersion = plugin.version.split('+')[0];

const failures = [];
if (packageJson.version !== pluginVersion) failures.push(`package ${packageJson.version} != plugin ${pluginVersion}`);
if (server.version !== packageJson.version) failures.push(`server ${server.version} != package ${packageJson.version}`);
if (server.name !== packageJson.mcpName) failures.push(`server name ${server.name} != mcpName ${packageJson.mcpName}`);
const npmPackage = (server.packages || []).find((item) => item.registryType === 'npm');
if (!npmPackage) failures.push('server.json does not declare an npm package');
else {
  if (npmPackage.identifier !== packageJson.name) failures.push(`registry package ${npmPackage.identifier} != ${packageJson.name}`);
  if (npmPackage.version !== packageJson.version) failures.push(`registry version ${npmPackage.version} != ${packageJson.version}`);
  if (npmPackage.transport?.type !== 'stdio') failures.push('registry npm package must use stdio');
}
if (!fs.existsSync(path.join(root, 'install.md'))) failures.push('install.md is missing');
if (failures.length) {
  for (const failure of failures) process.stderr.write(`- ${failure}\n`);
  process.exit(1);
}
process.stdout.write(`Code Relay npm and MCP Registry contracts verified: ${packageJson.version}\n`);
