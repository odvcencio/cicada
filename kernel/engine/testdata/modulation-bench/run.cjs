// Build the probe with:
// tinygo build -tags 'modular mod_benchmark' -target=wasm-unknown -opt=2 \
//   -panic=trap -no-debug -gc=leaking -scheduler=none -o probe.wasm \
//   ./kernel/engine/testdata/modulation-bench
// Then run: node kernel/engine/testdata/modulation-bench/run.cjs probe.wasm
// Set CICADA_PLAYWRIGHT_MODULE and CICADA_CHROME_PATH if needed.
const fs = require('fs');
const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');

async function main() {
  if (!process.argv[2]) throw new Error('pass the probe.wasm path');
  const browser = await chromium.launch({
    headless: true,
    executablePath: process.env.CICADA_CHROME_PATH,
    args: ['--no-sandbox'],
  });
  try {
    const page = await browser.newPage();
    const bytes = fs.readFileSync(process.argv[2]).toString('base64');
    const result = await page.evaluate(async encoded => {
      const bytes = Uint8Array.from(atob(encoded), c => c.charCodeAt(0));
      const {instance} = await WebAssembly.instantiate(bytes, {});
      const probe = instance.exports;
      probe._initialize();
      if (probe.modulation_probe_setup() !== 0) throw new Error('engine setup failed');
      for (let i = 0; i < 2; i++) probe.modulation_probe_run(100000);
      const iterations = 200000;
      const microsecondsPerGrid = [];
      for (let i = 0; i < 7; i++) {
        const start = performance.now();
        probe.modulation_probe_run(iterations);
        microsecondsPerGrid.push((performance.now() - start) * 1000 / iterations);
      }
      return {userAgent: navigator.userAgent, iterations, microsecondsPerGrid};
    }, bytes);
    console.log(JSON.stringify(result, null, 2));
  } finally {
    await browser.close();
  }
}

main().catch(err => { console.error(err); process.exitCode = 1; });
