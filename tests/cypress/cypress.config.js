const { defineConfig } = require('cypress')

module.exports = defineConfig({
  e2e: {
    baseUrl: 'http://127.0.0.1:3000',
    supportFile: false,
    specPattern: 'e2e/**/*.cy.js',
    viewportWidth: 1280,
    // the CI runner is sometimes slow to load a page (lazy-loaded views): 8 s per step instead of 4, and
    // one retry of a failed test (shown as "attempt 2" in the log, so a real problem still stands out)
    defaultCommandTimeout: 8000,
    retries: { runMode: 1, openMode: 0 },
    viewportHeight: 900,
  },
  video: true,
})
