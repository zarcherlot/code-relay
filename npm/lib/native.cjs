'use strict';

const crypto = require('node:crypto');
const fs = require('node:fs');
const http = require('node:http');
const https = require('node:https');
const os = require('node:os');
const path = require('node:path');

const ASSETS = Object.freeze({
  'linux-x64': 'code-relay-agent-linux-amd64',
  'linux-arm64': 'code-relay-agent-linux-arm64',
  'darwin-x64': 'code-relay-agent-darwin-amd64',
  'darwin-arm64': 'code-relay-agent-darwin-arm64',
  'win32-x64': 'code-relay-agent-windows-amd64.exe'
});

function resolveAsset(platform = process.platform, arch = process.arch) {
  const asset = ASSETS[`${platform}-${arch}`];
  if (!asset) {
    throw new Error(`Unsupported platform for Code Relay: ${platform}/${arch}`);
  }
  return asset;
}

function defaultCacheRoot(platform = process.platform, env = process.env) {
  if (env.CODE_RELAY_CACHE_DIR) return path.resolve(env.CODE_RELAY_CACHE_DIR);
  if (platform === 'win32') {
    return path.join(env.LOCALAPPDATA || path.join(os.homedir(), 'AppData', 'Local'), 'code-relay', 'Cache');
  }
  if (platform === 'darwin') return path.join(os.homedir(), 'Library', 'Caches', 'code-relay');
  return path.join(env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache'), 'code-relay');
}

function parseChecksums(text) {
  const values = new Map();
  for (const line of text.split(/\r?\n/)) {
    const match = /^([a-fA-F0-9]{64})\s+\*?(.+)$/.exec(line.trim());
    if (match) values.set(match[2], match[1].toLowerCase());
  }
  return values;
}

function sha256(data) {
  return crypto.createHash('sha256').update(data).digest('hex');
}

function requestBuffer(url, maxBytes, redirects = 5) {
  return new Promise((resolve, reject) => {
    const parsed = new URL(url);
    const transport = parsed.protocol === 'https:' ? https : parsed.protocol === 'http:' ? http : null;
    if (!transport) return reject(new Error(`Unsupported download protocol: ${parsed.protocol}`));
    const request = transport.get(parsed, { headers: { 'User-Agent': 'code-relay-mcp' } }, (response) => {
      if (response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
        response.resume();
        if (redirects === 0) return reject(new Error('Too many download redirects'));
        return resolve(requestBuffer(new URL(response.headers.location, parsed).toString(), maxBytes, redirects - 1));
      }
      if (response.statusCode !== 200) {
        response.resume();
        return reject(new Error(`Download failed with HTTP ${response.statusCode}: ${parsed}`));
      }
      const chunks = [];
      let size = 0;
      response.on('data', (chunk) => {
        size += chunk.length;
        if (size > maxBytes) {
          request.destroy(new Error(`Download exceeds ${maxBytes} bytes: ${parsed}`));
          return;
        }
        chunks.push(chunk);
      });
      response.on('end', () => resolve(Buffer.concat(chunks)));
      response.on('error', reject);
    });
    request.setTimeout(30_000, () => request.destroy(new Error(`Download timed out: ${parsed}`)));
    request.on('error', reject);
  });
}

function validCachedBinary(binaryPath, markerPath) {
  try {
    const expected = fs.readFileSync(markerPath, 'utf8').trim().toLowerCase();
    if (!/^[a-f0-9]{64}$/.test(expected)) return false;
    return sha256(fs.readFileSync(binaryPath)) === expected;
  } catch {
    return false;
  }
}

async function acquireLock(lockPath) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    try {
      return fs.openSync(lockPath, 'wx', 0o600);
    } catch (error) {
      if (error.code !== 'EEXIST') throw error;
      try {
        const age = Date.now() - fs.statSync(lockPath).mtimeMs;
        if (age > 5 * 60_000) fs.rmSync(lockPath, { force: true });
      } catch {}
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  }
  throw new Error(`Timed out waiting for download lock: ${lockPath}`);
}

async function ensureAgent(options = {}) {
  const version = options.version;
  if (!/^\d+\.\d+\.\d+(?:[-.][0-9A-Za-z.-]+)?$/.test(version || '')) {
    throw new Error(`Invalid Code Relay package version: ${version}`);
  }
  if (process.env.CODE_RELAY_AGENT_PATH) {
    const configured = path.resolve(process.env.CODE_RELAY_AGENT_PATH);
    if (!fs.statSync(configured).isFile()) throw new Error(`CODE_RELAY_AGENT_PATH is not a file: ${configured}`);
    return configured;
  }

  const asset = resolveAsset(options.platform, options.arch);
  const cacheRoot = options.cacheRoot || defaultCacheRoot(options.platform);
  const destinationDir = path.join(cacheRoot, version);
  const binaryPath = path.join(destinationDir, asset);
  const markerPath = `${binaryPath}.sha256`;
  if (validCachedBinary(binaryPath, markerPath)) return binaryPath;

  fs.mkdirSync(destinationDir, { recursive: true, mode: 0o700 });
  const lockPath = path.join(destinationDir, '.download.lock');
  const lock = await acquireLock(lockPath);
  try {
    if (validCachedBinary(binaryPath, markerPath)) return binaryPath;
    const baseUrl = options.baseUrl || `https://github.com/zarcherlot/code-relay/releases/download/v${version}`;
    process.stderr.write(`Code Relay: downloading verified ${asset} for ${version}\n`);
    const checksumData = await requestBuffer(`${baseUrl}/SHA256SUMS`, 1024 * 1024);
    const expected = parseChecksums(checksumData.toString('utf8')).get(asset);
    if (!expected) throw new Error(`SHA256SUMS does not contain ${asset}`);
    const binary = await requestBuffer(`${baseUrl}/${asset}`, 100 * 1024 * 1024);
    const actual = sha256(binary);
    if (actual !== expected) throw new Error(`Checksum mismatch for ${asset}`);

    const temporary = `${binaryPath}.${process.pid}.${crypto.randomBytes(4).toString('hex')}.tmp`;
    fs.writeFileSync(temporary, binary, { mode: 0o700 });
    if (process.platform !== 'win32') fs.chmodSync(temporary, 0o700);
    fs.rmSync(binaryPath, { force: true });
    fs.renameSync(temporary, binaryPath);
    fs.writeFileSync(markerPath, `${expected}\n`, { mode: 0o600 });
    return binaryPath;
  } finally {
    fs.closeSync(lock);
    fs.rmSync(lockPath, { force: true });
  }
}

module.exports = { ASSETS, defaultCacheRoot, ensureAgent, parseChecksums, resolveAsset, sha256 };
