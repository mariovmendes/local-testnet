import { Worker } from 'node:worker_threads';
import fs from 'node:fs';

const accounts = JSON.parse(fs.readFileSync('accounts.json', 'utf8'));
const resultAccounts = [];
accounts.forEach(element => {
  element.nonce ??= 0;
  element.recvNonce ??= 0;
  element.balance = 1000;
});

// split into chunks
function chunkArray(arr, numChunks) {
  const chunks = [];
  const size = Math.ceil(arr.length / numChunks);
  for (let i = 0; i < arr.length; i += size) {
    chunks.push(arr.slice(i, i + size));
  }
  return chunks;
}

const numWorkers = 1;
const chunks = chunkArray(accounts, numWorkers);
const workers = [];
const workerFinished = chunks.map(() => false);
const workerStats = chunks.map(() => ({ processed: 0, submitted: 0, failed: 0 }));
let shuttingDown = false;
let submitted = 0
let failed = 0

console.log(`Loaded ${accounts.length} accounts, spawning ${numWorkers} workers`);

chunks.forEach((chunk, index) => {
  console.log(`  worker ${index}: ${chunk.length} accounts`);

  const worker = new Worker('./worker.js', {
    workerData: { chunk, workerId: index }
  });

  workers[index] = worker;

  worker.on('message', (result) => {
    if (result.type === 'progress') {
      workerStats[index] = result.data;
    }

    if (result.type === 'done' || result.type === 'partial') {
      workerStats[index] = { processed: result.data.submitted + result.data.failed, submitted: result.data.submitted, failed: result.data.failed };
      result.data.accounts.forEach(element => resultAccounts.push(element));
      submitted += result.data.submitted;
      failed += result.data.failed;
    }

    if (result.type === 'error') {
      console.error(`Worker ${index} tx error:`, result.data);
    }

    if (result.type === 'done' || result.type === 'partial') {
      workerFinished[index] = true;
    }

    if (result.type === 'done') {
      worker.terminate();
    }
  });

  worker.on('error', (err) => {
    console.error(`Worker ${index} error:`, err);
  });

  worker.on('exit', (code) => {
    if (code !== 0) console.error(`Worker ${index} stopped with exit code ${code}`);
    // Can't wait forever on a dead worker — treat exit as its final report.
    workerFinished[index] = true;
  });
});

function saveAccounts() {
  if (resultAccounts.length === 0 && accounts.length > 0) {
    console.error(`  Refusing to overwrite accounts.json: 0 of ${accounts.length} accounts were reported back by workers. Leaving the existing file untouched.`);
    return;
  }
  if (resultAccounts.length < accounts.length) {
    console.error(`  WARNING: only ${resultAccounts.length}/${accounts.length} accounts reported back — saving partial data.`);
  }
  fs.writeFileSync('accounts.json', JSON.stringify(resultAccounts))
  console.log(`  Saved nonces for ${resultAccounts.length} accounts to accounts.json`)
}

const RUN_DURATION_MS = 30 * 60 * 1000; // 30 minutes
const startTime = Date.now()
console.log(`  Will run for ${RUN_DURATION_MS / 60_000} minutes, stopping at ${new Date(startTime + RUN_DURATION_MS).toLocaleTimeString()}`);

const progressTimer = setInterval(() => {
  const elapsed = ((Date.now() - startTime) / 1000).toFixed(1)
  const totals = workerStats.reduce((acc, s) => ({
    processed: acc.processed + s.processed,
    submitted: acc.submitted + s.submitted,
    failed: acc.failed + s.failed,
  }), { processed: 0, submitted: 0, failed: 0 })
  const rate = elapsed > 0 ? (totals.submitted / elapsed).toFixed(1) : '0'
  const breakdown = workerStats.map((s, i) => `w${i}: processed=${s.processed} submitted=${s.submitted} failed=${s.failed}`).join('  |  ')
  console.log(`  [${elapsed}s] total submitted=${totals.submitted} failed=${totals.failed} (${rate} tx/s)  ${breakdown}`)
}, 5000)

function shutdown(signal) {
  if (shuttingDown) return;
  shuttingDown = true;
  console.log(`\n${signal} received, asking workers to stop...`);
  clearInterval(progressTimer);
  clearTimeout(durationTimer);
  workers.forEach(worker => {
    worker.postMessage({ type: 'stop' });
  });

  const maxWaitMs = 15_000
  const pollMs = 200
  const deadline = Date.now() + maxWaitMs

  const waitForWorkers = setInterval(() => {
    const allDone = workerFinished.every(Boolean)
    if (allDone || Date.now() > deadline) {
      clearInterval(waitForWorkers)
      if (!allDone) {
        console.error(`  WARNING: timed out after ${maxWaitMs}ms waiting for all workers to report; some in-flight accounts may be missing from accounts.json`);
      }
      const elapsed = ((Date.now() - startTime) / 1000).toFixed(1)
      const avgRate = elapsed > 0 ? (submitted / Number.parseFloat(elapsed) / numWorkers).toFixed(1) : '0'
      const avgFullRate = elapsed > 0 ? ((submitted + failed) / Number.parseFloat(elapsed) / numWorkers).toFixed(1) : '0'
      console.log(`\n\n  ────────────────────────────────────`)
      console.log(`  submitted: ${submitted}`)
      console.log(`  failed:    ${failed}`)
      console.log(`  ${elapsed}s · avg ${avgRate} tx/s per worker`)
      console.log(`  Full avg rate: ${avgFullRate} tx/s per worker`)
      console.log(`  ────────────────────────────────────`)
      saveAccounts()
      process.exit(0)
    }
  }, pollMs)
}

process.on('SIGINT', () => shutdown('SIGINT'));
process.on('SIGTERM', () => shutdown('SIGTERM'));

const durationTimer = setTimeout(() => shutdown('TIME_LIMIT'), RUN_DURATION_MS);