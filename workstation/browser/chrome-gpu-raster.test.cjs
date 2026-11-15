'use strict';
// chrome-gpu-raster.cjs gives a suite's Chrome GPU rasterization, so tile
// raster tasks run one at a time and Chrome's software-raster freeze
// (described at the top of chrome-gpu-raster.cjs) cannot happen. On a runner
// without a GPU the same Chrome rasterizes in software unless these flags are
// set.
const test = require('node:test');
const assert = require('node:assert/strict');
const gpu = require('./chrome-gpu-raster.cjs');

test('suites get GPU rasterization unless they choose their own graphics backend', () => {
  assert.deepEqual(gpu.withGpuRaster({headless: true, args: ['--no-sandbox']}), {headless: true, args: ['--no-sandbox', ...gpu.flags]});
  assert.deepEqual(gpu.withGpuRaster().args, gpu.flags);
  assert.deepEqual(gpu.withGpuRaster(gpu.withGpuRaster({args: []})).args, gpu.flags, 'applying it twice adds nothing');
  for (const own of ['--use-gl=angle', '--use-angle=gl-egl', '--disable-gpu']) {
    const options = {args: ['--no-sandbox', own]};
    assert.equal(gpu.withGpuRaster(options), options, `${own} is kept as the suite set it`);
  }
});

test("CI's Chrome rasterizes on the GPU with these flags", {skip: !process.env.CICADA_CHROME_PATH && 'needs CICADA_CHROME_PATH', timeout: 60000}, async t => {
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  const status = async args => {
    const browser = await chromium.launch({headless: true, executablePath: process.env.CICADA_CHROME_PATH, args});
    try {
      const {gpu: info} = await (await browser.newBrowserCDPSession()).send('SystemInfo.getInfo');
      return {compositing: info.featureStatus.gpu_compositing, rasterization: info.featureStatus.rasterization};
    } finally {
      await browser.close();
    }
  };
  const baseline = await status(['--no-sandbox', '--mute-audio']);
  t.diagnostic(`without the flags: compositing ${baseline.compositing}, rasterization ${baseline.rasterization}`);
  const configured = await status(gpu.withGpuRaster({args: ['--no-sandbox', '--mute-audio']}).args);
  t.diagnostic(`with the flags: compositing ${configured.compositing}, rasterization ${configured.rasterization}`);
  assert.match(configured.compositing, /^enabled/, `GPU compositing: ${configured.compositing}`);
  assert.match(configured.rasterization, /^enabled/, `GPU rasterization: ${configured.rasterization}`);
});
