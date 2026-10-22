'use strict';
// CI starts the normal local app. Claim its printed, one-use owner capability
// for the first context only; collaboration peers must remain separate viewers.
const fs = require('node:fs');

function startupURL(log, origin) {
  const match = log.match(/^Cicada Studio: (http:\/\/[^\s]+\/__local\/[A-Za-z0-9_-]+)\s*$/m);
  if (!match) throw new Error('Studio did not print its local owner startup URL');
  const url = new URL(match[1]);
  if (url.origin !== new URL(origin).origin || !['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)) {
    throw new Error('Studio owner startup URL must match the disposable loopback app');
  }
  return url.href;
}

function install(browserType, readStartup) {
  const launch = browserType.launch.bind(browserType);
  let available = true;
  browserType.launch = async function (...args) {
    const browser = await launch(...args);
    const newContext = browser.newContext.bind(browser);
    browser.newContext = async function (...options) {
      // Reserve before awaiting: concurrent collaboration contexts still give
      // the first caller the owner identity, rather than the fastest context.
      const owner = available;
      available = false;
      const context = await newContext(...options);
      if (owner) {
        try {
          const response = await context.request.get(readStartup());
          if (!response.ok()) throw new Error('Studio owner bootstrap failed: HTTP ' + response.status());
        } catch (error) {
          await context.close();
          throw error;
        }
      }
      return context;
    };
    return browser;
  };
}

module.exports = {startupURL, install};
if (process.env.CICADA_STUDIO_STARTUP_LOG) {
  const {chromium} = require(process.env.CICADA_PLAYWRIGHT_MODULE || 'playwright');
  install(chromium, () => startupURL(fs.readFileSync(process.env.CICADA_STUDIO_STARTUP_LOG, 'utf8'), process.env.CICADA_STUDIO_URL));
}
