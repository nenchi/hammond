const { defineConfig } = require('cypress')

module.exports = defineConfig({
  e2e: {
    baseUrl: 'http://127.0.0.1:3000',
    supportFile: false,
    specPattern: 'e2e/**/*.cy.js',
    viewportWidth: 1280,
    viewportHeight: 900,
  },
  video: true,
})
