// UI smoke test in a real browser: catches what the API smoke test can't (Vue/buefy/date-fns/chart.js
// breaking at runtime after a dependency update). Seeds its own data through the API.
// throwaway credentials of the CI container (Cypress.env() was removed in Cypress 16)
const email = 'admin@example.com'
const password = 'smoke-test-pass'
let vehicleId

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

  it('new fill-up form renders with a date', () => {
    visit(`/vehicles/${vehicleId}/fillup`)
    cy.get('input[name=username]').should('not.exist')
    cy.get('.datepicker input').invoke('val').should('match', /\d/)
    noConsoleErrors()
  })

  // upstream bug: on a phone the Stats chart kept reloading and growing forever
  it('mobile: stats chart loads once and keeps its size', () => {
    cy.viewport(375, 667)
    cy.intercept('GET', '/api/vehicles/*/mileage*').as('mileage')
    visit(`/vehicles/${vehicleId}`)
    cy.contains('h1', 'Stats').scrollIntoView()
    cy.wait('@mileage')
    cy.get('canvas').should('be.visible').then(($c) => {
      const first = $c[0].getBoundingClientRect().height
      cy.wait(3000) // give a resize/refetch loop time to show itself
      cy.get('canvas').then(($c2) => {
        const later = $c2[0].getBoundingClientRect().height
        expect(later, 'chart height after 3 s').to.be.within(first - 1, first + 1)
        expect(later, 'chart height').to.be.lessThan(400)
      })
    })
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
