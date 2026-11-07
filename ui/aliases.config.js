const path = require('path')

const aliases = {
  '@': '.',
  '@src': 'src',
  '@router': 'src/router',
  '@views': 'src/router/views',
  '@layouts': 'src/router/layouts',
  '@components': 'src/components',
  '@assets': 'src/assets',
  '@utils': 'src/utils',
  '@state': 'src/state',
  '@design': 'src/design/index.scss',
}

module.exports = { webpack: {} }
for (const alias in aliases) {
  module.exports.webpack[alias] = path.resolve(__dirname, aliases[alias])
}
