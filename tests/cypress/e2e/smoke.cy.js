// UI smoke test in a real browser: catches what the API smoke test can't (Vue/buefy/date-fns/chart.js
// breaking at runtime after a dependency update). Seeds its own data through the API.
// throwaway credentials of the CI container (Cypress.env() was removed in Cypress 16)
const email = 'admin@example.com'
const password = 'smoke-test-pass'
let vehicleId
let apiHeaders
const baseSettings = { currency: 'EUR', distanceUnit: 1, dateFormat: 'dd/MM/yyyy' }

// Vue logs render errors with console.error instead of throwing: fail on any of them.
const visit = (url) =>
  cy.visit(url, { onBeforeLoad: (win) => cy.spy(win.console, 'error').as('consoleError') })
const noConsoleErrors = () => cy.get('@consoleError').should('not.have.been.called')

const fillLogin = (pw) => {
  cy.get('input[name=username]', { timeout: 10000 }).clear().type(email)
  cy.get('input[name=password]').clear().type(pw, { log: false })
  cy.get('form input[type=submit]').click()
}
const loginUI = (pw) => {
  visit('/login')
  fillLogin(pw)
}

before(() => {
  cy.request('/api/system/status').then(({ body }) => {
    if (!body.initialized) {
      cy.request('POST', '/api/auth/initialize', {
        name: 'Admin', email, password, currency: 'EUR', distanceUnit: 1,
      })
    }
  })
  cy.request('POST', '/api/login', { email, password }).then(({ body }) => {
    const headers = { Authorization: body.token }
    apiHeaders = headers
    cy.request({ url: '/api/me', headers }).then(({ body: me }) => {
      cy.request({
        method: 'POST', url: '/api/vehicles', headers,
        body: { nickname: 'Cypress', registration: 'CY 1234', make: 'Skoda', model: 'Octavia',
                yearOfManufacture: 2020, engineSize: 1.6, fuelUnit: 0, fuelType: 1 },
      }).then(({ body: v }) => {
        vehicleId = v.id
        ;[[20000, 40], [20640, 42.5]].forEach(([odo, qty]) =>
          cy.request({
            method: 'POST', url: `/api/vehicles/${v.id}/fillups`, headers,
            body: { vehicleId: v.id, fuelUnit: 0, fuelQuantity: qty, perUnitPrice: 1.55,
                    totalAmount: +(qty * 1.55).toFixed(2), odoReading: odo, isTankFull: true,
                    fillingStation: 'Cypress Station', userId: me.id, date: '2026-11-07T10:00:00Z' },
          }))
      })
    })
  })
})

describe('hammond UI', () => {
  beforeEach(() => {
    loginUI(password)
    cy.location('pathname').should('eq', '/')
  })

  it('home lists the vehicle', () => {
    cy.contains('h1', 'Your Vehicles')
    cy.contains('Cypress - CY 1234')
    noConsoleErrors()
  })

  it('vehicle page shows fill-ups with formatted dates', () => {
    // the card title only expands/collapses the card; the link is "Details" in its footer
    cy.get(`a.card-footer-item[href="/vehicles/${vehicleId}"]`).contains('Details').click()
    cy.location('pathname').should('eq', `/vehicles/${vehicleId}`)
    cy.contains('p.title', 'Cypress - CY 1234')
    cy.contains('h1', 'Past Fillups')
    // date-fns parseISO + format('MMM dd, yyyy')
    cy.contains('td', 'Nov 07, 2026')
    cy.contains('td', /^\s*20640 km\s*$/i) // app shows "Km"
    cy.contains('td', 'Cypress Station')
    noConsoleErrors()
  })

  it('new fill-up form renders with a date, pre-filled from the last fill-up', () => {
    visit(`/vehicles/${vehicleId}/fillup`)
    cy.get('input[name=username]').should('not.exist')
    cy.get('.datepicker input').invoke('val').should('match', /\d/)
    // price + station copied, odometer only as a hint (a copied odometer would make the mileage wrong)
    cy.contains('.field', 'Price per').find('input').should('have.value', '1.55')
    cy.contains('.field', 'Filling Station Name').find('input').should('have.value', 'Cypress Station')
    cy.contains('.field', 'Odometer Reading').find('input').should('have.value', '').and('have.attr', 'placeholder', 'last: 20640')
    cy.contains('Last fill-up: 20640')
    noConsoleErrors()
  })

  it('pre-fill can be turned off in the settings', () => {
    cy.request({ method: 'POST', url: '/api/me/settings', headers: apiHeaders, body: { ...baseSettings, prefillFillup: false } })
    visit(`/vehicles/${vehicleId}/fillup`)
    cy.contains('Last fill-up: 20640') // the hint stays
    cy.contains('.field', 'Price per').find('input').should('have.value', '')
    cy.contains('.field', 'Filling Station Name').should('not.exist') // not pre-filled, section stays closed
    cy.request({ method: 'POST', url: '/api/me/settings', headers: apiHeaders, body: { ...baseSettings, prefillFillup: true } })
  })

  it('dark theme can be chosen in the settings and colours the app', () => {
    visit('/settings')
    cy.contains('.field', 'Theme').find('select').select('dark')
    cy.get('form input[type=submit][value=Save]').first().click()
    cy.contains('Settings saved successfully')
    cy.get('html').should('have.class', 'theme-dark')
    visit(`/vehicles/${vehicleId}`)
    cy.get('html').should('have.class', 'theme-dark')
    cy.get('body').should('have.css', 'background-color', 'rgb(22, 25, 29)')
    cy.get('.box').first().should('have.css', 'background-color', 'rgb(31, 35, 40)')
    cy.get('canvas').should('be.visible')
    noConsoleErrors()
    cy.request({ method: 'POST', url: '/api/me/settings', headers: apiHeaders, body: { ...baseSettings, theme: 'light' } })
    visit('/')
    cy.get('html').should('not.have.class', 'theme-dark')
  })

  // upstream bug: on a phone the Stats chart kept reloading and growing forever
  it('mobile: stats chart loads once and keeps its size', () => {
    cy.viewport(375, 667)
    cy.intercept('GET', '/api/vehicles/*/mileage*').as('mileage')
    visit(`/vehicles/${vehicleId}`)
    cy.contains('h1', 'Stats').scrollIntoView()
    cy.wait('@mileage')
    // Chart.js starts at its default 400px canvas and then sizes to the 300px box: wait until it has
    // settled (measuring right away was flaky), then make sure it stays (the old bug kept growing)
    const height = ($c) => $c[0].getBoundingClientRect().height
    cy.get('canvas').should(($c) => expect(height($c), 'chart settled at its 300px box').to.be.within(299, 301))
    cy.wait(3000) // give a resize/refetch loop time to show itself
    cy.get('canvas').then(($c) => expect(height($c), 'chart height after 3 s').to.be.within(299, 301))
    cy.get('@mileage.all').should('have.length.at.most', 1)
    noConsoleErrors()
  })

  it('logs out, and a wrong password is refused', () => {
    visit('/logout')
    cy.location('pathname').should('eq', '/login')
    // use the login page logout landed on; reloading /login right away raced the logout (flaky in CI)
    fillLogin('wrong-password')
    cy.contains('There was an error logging in')
    cy.location('pathname').should('eq', '/login')
  })
})
