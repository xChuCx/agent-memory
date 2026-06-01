#!/usr/bin/env node
'use strict';

// Thin launcher for agent-memory. On first run it downloads the prebuilt
// binary for this platform from the matching GitHub release, verifies its
// SHA-256 against the release checksums file, caches it, then execs it with
// every argument forwarded. No npm dependencies — Node built-ins only.
//
// MCP clients launch the server with:
//   npx -y @xchucx/agent-memory mcp --root .
// The first launch downloads once per version; later launches are instant.

const { spawnSync } = require('node:child_process');
const crypto = require('node:crypto');
const fs = require('node:fs');
const https = require('node:https');
const os = require('node:os');
const path = require('node:path');

const REPO = 'xChuCx/agent-memory';
const BIN = 'agent-memory';
const { version } = require('../package.json');

const GOOS = { linux: 'linux', darwin: 'darwin', win32: 'windows' };
const GOARCH = { x64: 'amd64', arm64: 'arm64' };

function platform() {
  const goos = GOOS[process.platform];
  const goarch = GOARCH[process.arch];
  if (!goos || !goarch) {
    throw new Error(
      `unsupported platform ${process.platform}/${process.arch}; ` +
        `download a binary from https://github.com/${REPO}/releases`,
    );
  }
  return { goos, goarch };
}

function fetch(url, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > 10) {
      reject(new Error('too many redirects'));
      return;
    }
    https
      .get(url, { headers: { 'User-Agent': `${BIN}-npm/${version}` } }, (res) => {
        const status = res.statusCode || 0;
        if (status >= 300 && status < 400 && res.headers.location) {
          res.resume();
          resolve(fetch(new URL(res.headers.location, url).toString(), redirects + 1));
          return;
        }
        if (status !== 200) {
          res.resume();
          reject(new Error(`GET ${url} -> HTTP ${status}`));
          return;
        }
        const chunks = [];
        res.on('data', (c) => chunks.push(c));
        res.on('end', () => resolve(Buffer.concat(chunks)));
      })
      .on('error', reject);
  });
}

// Locate this platform's raw-binary asset in the goreleaser checksums file.
// Returns { name, sha256 } and drives both the download URL and the integrity
// check, so the exact filename (incl. a possible .exe) is never guessed.
function findAsset(checksums, goos, goarch) {
  const want = `${BIN}_${version}_${goos}_${goarch}`;
  for (const line of checksums.split('\n')) {
    const m = line.trim().match(/^([a-f0-9]{64})\s+(.+)$/i);
    if (!m) continue;
    const name = m[2];
    if (name === want || name === `${want}.exe`) {
      return { name, sha256: m[1].toLowerCase() };
    }
  }
  throw new Error(
    `no raw binary for ${goos}/${goarch} in release v${version} ` +
      `(expected ${want}); the release may predate this npm wrapper`,
  );
}

async function ensureBinary() {
  const { goos, goarch } = platform();
  const cacheDir = path.join(os.homedir(), '.cache', BIN, version);
  const cached = path.join(cacheDir, goos === 'windows' ? `${BIN}.exe` : BIN);
  if (fs.existsSync(cached)) return cached;

  const base = `https://github.com/${REPO}/releases/download/v${version}`;
  const checksums = (await fetch(`${base}/${BIN}_${version}_checksums.txt`)).toString('utf8');
  const asset = findAsset(checksums, goos, goarch);
  const bin = await fetch(`${base}/${asset.name}`);

  const got = crypto.createHash('sha256').update(bin).digest('hex');
  if (got !== asset.sha256) {
    throw new Error(`checksum mismatch for ${asset.name}: expected ${asset.sha256}, got ${got}`);
  }

  fs.mkdirSync(cacheDir, { recursive: true });
  const tmp = `${cached}.${process.pid}.tmp`;
  fs.writeFileSync(tmp, bin, { mode: 0o755 });
  fs.renameSync(tmp, cached);
  return cached;
}

async function main() {
  let bin;
  try {
    bin = await ensureBinary();
  } catch (err) {
    process.stderr.write(`agent-memory: ${err.message}\n`);
    process.stderr.write(`Install manually from https://github.com/${REPO}/releases\n`);
    process.exit(1);
  }
  const r = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
  if (r.error) {
    process.stderr.write(`agent-memory: ${r.error.message}\n`);
    process.exit(1);
  }
  process.exit(typeof r.status === 'number' ? r.status : r.signal ? 1 : 0);
}

// Run only when invoked directly (npx / bin); stay importable for tests.
if (require.main === module) {
  main();
}

module.exports = { platform, findAsset, ensureBinary };
