'use strict';
// chrome-gpu-raster.cjs: Studio's browser suites in CI run Chrome with GPU
// compositing and GPU rasterization, on SwiftShader when the runner has no GPU.
//
// Why: without a GPU, headless Chrome 152 rasterizes tiles in software, and
// those raster tasks run on several worker threads at once. A page can then
// stop for good: a pending layer tree waits for its last raster task, that
// task finishes as the worker yields to V8's garbage collector, the task that
// should mark the tree ready to activate is never run, and the next commit
// never completes. The page's main thread then waits forever in
// LayerTreeHost::WaitForCommitCompletion; nothing in JavaScript can be
// interrupted. producer-workflow froze this way in CI after its first click on
// the Session page. With GPU rasterization Chrome runs tile raster tasks one at
// a time, and the page renders the way it does on a machine with a GPU.
//
// CI loads this file into every suite with NODE_OPTIONS=--require=...
// (scripts/ci-studio-browser.sh). A suite that chooses its own graphics
// backend (--use-gl, --use-angle or --disable-gpu) keeps its own flags.
const flags = ['--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist', '--enable-gpu-rasterization'];

function withGpuRaster(options = {}) {
  const args = Array.isArray(options.args) ? options.args : [];
  if (args.some(arg => /^--(use-gl|use-angle|disable-gpu)(=|$)/.test(arg))) return options;
  return {...options, args: [...args, ...flags]};
}

function install(browserType) {
  const launch = browserType.launch.bind(browserType);
  browserType.launch = (options, ...rest) => launch(withGpuRaster(options), ...rest);
}

module.exports = {flags, withGpuRaster, install};
if (process.env.CICADA_PLAYWRIGHT_MODULE) install(require(process.env.CICADA_PLAYWRIGHT_MODULE).chromium);
