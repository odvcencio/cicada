'use strict';
// chrome-gpu-raster.cjs: Chrome flags for GPU compositing and GPU
// rasterization, on SwiftShader when the machine has no GPU.
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
// a time.
//
// A suite opts in by passing its launch options through withGpuRaster(). It is
// not applied to every suite: SwiftShader rendering is slower, and in CI it
// made shard 1 take 496 s instead of 268 s and broke three suites that depend
// on frame timing (pull request #355). A suite that chooses its own graphics
// backend (--use-gl, --use-angle or --disable-gpu) keeps its own flags.
const flags = ['--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist', '--enable-gpu-rasterization'];

function withGpuRaster(options = {}) {
  const args = Array.isArray(options.args) ? options.args : [];
  if (args.some(arg => /^--(use-gl|use-angle|disable-gpu)(=|$)/.test(arg))) return options;
  return {...options, args: [...args, ...flags]};
}

module.exports = {flags, withGpuRaster};
