#!/usr/bin/env node
// Smoke-test the extracted release binary against disposable application data.
// This test never launches a browser or downloads media.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, rm, stat } from 'node:fs/promises';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

const executable = process.argv[2];
if (!executable) throw new Error('Usage: node scripts/release-smoke.mjs <extracted-executable>');
for (const name of ['RELEASE_VERSION', 'COMMIT', 'BUILD_DATE']) {
  if (!process.env[name]) throw new Error(`Missing release identity variable ${name}`);
}

async function availablePort() {
  const socket = net.createServer();
  socket.listen(0, '127.0.0.1');
  await once(socket, 'listening');
  const { port } = socket.address();
  await new Promise((resolve, reject) => socket.close(err => err ? reject(err) : resolve()));
  return port;
}

const temp = await mkdtemp(path.join(os.tmpdir(), 'yt-dl-go-release-smoke-'));
const dataDir = path.join(temp, 'private-data');
let child;
let exited;
let output = '';

async function stopChild() {
  if (!child) return;
  if (child.exitCode === null && child.signalCode === null) {
    // Go handles SIGINT on Unix; Node terminates Windows processes directly.
    // Graceful Windows console shutdown remains a manual release gate.
    child.kill(process.platform === 'win32' ? 'SIGTERM' : 'SIGINT');
  }
  let timer;
  try {
    const result = await Promise.race([
      exited,
      new Promise((_, reject) => {
        timer = setTimeout(() => {
          child.kill('SIGKILL');
          reject(new Error('Release service failed to stop within 12 seconds'));
        }, 12000);
      }),
    ]);
    child = undefined;
    return result;
  } finally {
    clearTimeout(timer);
  }
}

async function smoke(round) {
  const port = await availablePort();
  const addr = `127.0.0.1:${port}`;
  const origin = `http://${addr}`;
  child = spawn(executable, ['--no-browser'], {
    windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
    env: {
      ...process.env, ADDR: addr, DATA_DIR: dataDir, NO_BROWSER: '1',
      API_TOKEN: '', ALLOWED_ORIGINS: '', ALLOWED_HOSTS: '',
    },
  });
  output = '';
  child.stdout.on('data', data => { output = (output + data.toString()).slice(-16000); });
  child.stderr.on('data', data => { output = (output + data.toString()).slice(-16000); });
  exited = new Promise(resolve => child.once('close', (code, signal) => resolve({ code, signal })));
  let spawnError;
  child.once('error', error => { spawnError = error; });

  try {
    let healthy = false;
    const deadline = Date.now() + 30000;
    while (Date.now() < deadline) {
      if (spawnError) throw spawnError;
      if (child.exitCode !== null || child.signalCode !== null) {
        throw new Error(`Service exited before healthy: ${output}`);
      }
      try {
        const response = await fetch(`${origin}/api/health`, { signal: AbortSignal.timeout(1500) });
        if (response.status === 200) {
          const health = await response.json();
          assert.equal(health.ready, true, `health.ready: ${JSON.stringify(health)}`);
          assert.equal(health.degraded, false);
          assert.equal(health.version, process.env.RELEASE_VERSION);
          assert.equal(health.commit, process.env.COMMIT);
          assert.equal(health.buildDate, process.env.BUILD_DATE);
          const ui = await fetch(`${origin}/`, { signal: AbortSignal.timeout(3000) });
          assert.equal(ui.status, 200, 'Release UI was not served');
          assert.match(ui.headers.get('content-type') ?? '', /text\/html/i);
          assert.match(await ui.text(), /<html\b/i);
          assert.equal(ui.headers.get('x-frame-options'), 'DENY');
          const database = await stat(path.join(dataDir, 'state.db'));
          assert.ok(database.size > 0, 'Private SQLite database is missing');
          healthy = true;
          break;
        }
      } catch (err) {
        // Connection-refused is expected during startup; never swallow
        // failed health/UI/security assertions once a response arrives.
        if (err?.code === 'ERR_ASSERTION') throw err;
        if (err?.cause?.code !== 'ECONNREFUSED' && err?.name !== 'TimeoutError' && err?.name !== 'TypeError') throw err;
      }
      await delay(250);
    }
    if (!healthy) throw new Error(`Release service timed out on ${origin}: ${output}`);
    console.log(`Release package session ${round}: healthy API, embedded UI, identity, SQLite`);
  } finally {
    const result = await stopChild();
    if (process.platform !== 'win32' && result?.code !== 0) {
      throw new Error(`Release service shutdown failed: ${JSON.stringify(result)} ${output}`);
    }
  }
}

try {
  await smoke(1);
  await smoke(2); // Restart against the same private database.
  console.log('Extracted release package startup/restart smoke test passed');
} finally {
  try { await stopChild(); } finally { await rm(temp, { recursive: true, force: true }); }
}
