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
                yearOfManufacture: 2020, engineSize: 1598, fuelUnit: 0, fuelType: 1 }, // engine size in cc: 1.6 is invalid in the form and blocked the save
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

  it('warns about an odometer typo in the form and lists saved ones on the vehicle page', () => {
    visit(`/vehicles/${vehicleId}/fillup`)
    cy.contains('.field', 'Odometer Reading').find('input').type('206400')
    cy.contains('185760 Km since the last fill-up. Typo?')
    cy.contains('.field', 'Odometer Reading').find('input').clear().type('21200')
    cy.contains('Typo?').should('not.exist')
    // a saved typo (dated before the other seeds, so later tests' "last fill-up" stays 20640)
    cy.request({ url: '/api/me', headers: apiHeaders }).then(({ body: me }) =>
      cy.request({
        method: 'POST', url: `/api/vehicles/${vehicleId}/fillups`, headers: apiHeaders,
        body: { vehicleId, fuelUnit: 0, fuelQuantity: 30, perUnitPrice: 1.5, totalAmount: 45, odoReading: 1950,
                isTankFull: true, userId: me.id, date: '2026-11-01T10:00:00Z' },
      })).then(({ body: f }) => {
        visit(`/vehicles/${vehicleId}`)
        cy.contains('.odo-warning', 'Odometer looks wrong').should('contain', '1950 Km').and('not.contain', '20000 Km')
        cy.get('tr.is-suspicious').should('have.length', 1)
        cy.request({ method: 'DELETE', url: `/api/vehicles/${vehicleId}/fillups/${f.id}`, headers: apiHeaders })
      })
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
  it('dark theme on a phone: fill-up cards are dark, not white', () => {
    cy.request({ method: 'POST', url: '/api/me/settings', headers: apiHeaders, body: { ...baseSettings, theme: 'dark' } })
    cy.viewport(375, 667)
    visit(`/vehicles/${vehicleId}`)
    cy.get('.b-table .table-wrapper.has-mobile-cards tbody tr').should('have.length.at.least', 2).each(($tr) =>
      expect(getComputedStyle($tr[0]).backgroundColor, 'card background').to.not.eq('rgb(255, 255, 255)')
    )
    cy.request({ method: 'POST', url: '/api/me/settings', headers: apiHeaders, body: { ...baseSettings, theme: 'light' } })
  })

  it('phone: API key list fits, Revoke is inside the box', () => {
    cy.request({ method: 'POST', url: '/api/me/apikeys', headers: apiHeaders, body: { name: 'phone layout' } })
    cy.viewport(375, 667)
    visit('/settings')
    cy.get('.api-keys').then(($box) => {
      const box = $box[0].getBoundingClientRect()
      cy.get('.api-key-list').contains('button', 'Revoke').then(($b) => {
        const b = $b[0].getBoundingClientRect()
        expect(b.right, 'Revoke inside the API keys box').to.be.at.most(box.right)
      })
    })
  })

  it('phone: the Stats range select stays inside the box', () => {
    cy.viewport(375, 667)
    visit(`/vehicles/${vehicleId}`)
    cy.get('.stats-range').then(($sel) => {
      const sel = $sel[0].getBoundingClientRect()
      const box = $sel.closest('.box')[0].getBoundingClientRect()
      expect(sel.left, 'left edge').to.be.at.least(box.left)
      expect(sel.right, 'right edge').to.be.at.most(box.right)
      expect(sel.top, 'below the box top').to.be.at.least(box.top)
    })
  })

  it('export button downloads the vehicle CSV; admins see the backup download', () => {
    visit(`/vehicles/${vehicleId}`)
    cy.get('a.export-csv').should('have.attr', 'href').then((href) =>
      cy.request(href).then(({ status, headers, body }) => {
        expect(status).to.eq(200)
        expect(headers['content-disposition']).to.match(/attachment; filename="hammond-Cypress-\d{4}-\d{2}-\d{2}\.csv"/)
        expect(body.split('\n')[0]).to.contain('Type,MPG,Date,Time,Car Name')
        expect(body).to.contain('Cypress Station')
      })
    )
    visit('/settings')
    cy.get('.backup').contains('a', 'Download full backup').should('have.attr', 'href').and('contain', '/api/backup?access_token=')
  })

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

  it('tank size can be set on the vehicle', () => {
    cy.intercept('PUT', `/api/vehicles/${vehicleId}`).as('saveVehicle')
    visit(`/vehicles/${vehicleId}/edit`)
    cy.contains('.field', 'Tank size').find('input.input').clear().type('43')
    cy.get('form [type=submit]').first().click()
    cy.wait('@saveVehicle').its('response.statusCode').should('eq', 200)
    cy.request({ url: `/api/vehicles/${vehicleId}`, headers: apiHeaders }).its('body.tankCapacity').should('eq', 43)
  })

  it('warns when a fill-up is more than the tank holds', () => {
    visit(`/vehicles/${vehicleId}/fillup`)
    cy.get('.qty-field input').type('54')
    cy.contains('54 is more than the tank holds (43)')
    cy.get('.qty-field input').clear().type('42')
    cy.contains('more than the tank holds').should('not.exist')
    noConsoleErrors()
  })

  it('detected fill-up: API key in settings, detection via API, complete it in the UI', () => {
    visit('/settings')
    cy.get('.api-keys').within(() => {
      cy.contains('.field', 'Name').find('input.input').clear().type('Cypress HA') // not the button next to it
      cy.get('form [type=submit]').click()
      cy.get('.created-key').invoke('text').should('match', /^hmd_[0-9a-f]{48}$/).as('key') // shown in full
      cy.contains('td', 'Cypress HA')
    })
    cy.get('@key').then((key) => {
      // what the Home Assistant blueprint sends: levels before/after + odometers
      cy.request({
        method: 'POST', url: `/api/v1/vehicles/${vehicleId}/detections`, headers: { Authorization: `Bearer ${key}` },
        body: { odoReading: 21300, odoAtBefore: 21298, levelBefore: 20, levelAfter: 98, date: '2026-11-07T14:00:00Z', source: 'Home Assistant' },
      }).then(({ body }) => {
        expect(body.result).to.eq('pending')
        expect(body.detection.fuelQuantity).to.eq(33.5) // 78 % of 43 l
        expect(body.message).to.contain('~33.5')
      })
    })
    visit('/')
    cy.contains('.navbar a', 'Detected').parent().contains('.tag', '1').click()
    cy.location('pathname').should('eq', '/detections')
    cy.get('.detection').should('have.length', 1).within(() => {
      cy.contains('~33.5')
      cy.contains('20% → 98%')
      cy.contains('Complete').click()
    })
    cy.location('search').should('contain', 'detection=')
    cy.get('.detection-note').should('contain', 'Home Assistant').and('contain', '33.5')
    cy.contains('.field', 'Odometer Reading').find('input').should('have.value', '21300')
    cy.contains('.field', 'Quantity').find('input[type=number]').first().should('have.value', '33.5')
    cy.contains('.field', 'Price per').find('input').clear().type('1.62')
    cy.get('form [type=submit]').first().click()
    cy.location('pathname').should('eq', '/detections')
    cy.contains('Nothing detected right now')
    cy.get('.navbar').should('not.contain', 'Detected')
    cy.request({ url: `/api/vehicles/${vehicleId}/fillups`, headers: apiHeaders })
      .its('body').should((fs) => expect(fs.map((f) => f.odoReading)).to.include(21300))
    noConsoleErrors()
  })

  it('detected duplicate: shows both, "Keep mine" leaves my entry alone', () => {
    cy.request({ method: 'POST', url: '/api/me/apikeys', headers: apiHeaders, body: { name: 'dup test' } }).then(({ body: k }) =>
      cy.request({
        method: 'POST', url: `/api/v1/vehicles/${vehicleId}/detections`, headers: { 'X-Api-Key': k.key },
        body: { odoReading: 20650, fuelQuantity: 41, date: '2026-11-07T12:00:00Z' },
      }).its('body.result').should('eq', 'duplicate')
    )
    visit('/detections')
    cy.get('.detection').should('have.length', 1).within(() => {
      cy.contains('.tag', 'possible duplicate')
      cy.contains('Your entry')
      cy.contains('20640')
      cy.contains('Keep mine').click()
    })
    cy.contains('Nothing detected right now')
    cy.request({ url: `/api/vehicles/${vehicleId}/fillups`, headers: apiHeaders })
      .its('body').should((fs) => expect(fs.find((f) => f.odoReading === 20640).fuelQuantity).to.eq(42.5))
  })

  it('settings show this version and the fork, crediting upstream', () => {
    visit('/settings')
    cy.get('.app-version').invoke('text').should('match', /^\d+\.\d+\.\d+$/)
    cy.contains('td', 'Website').next().find('a').should('have.attr', 'href', 'https://github.com/nenchi/hammond')
    cy.contains('td', 'Found a bug').next().find('a').should('have.attr', 'href', 'https://github.com/nenchi/hammond/issues')
    cy.contains('td', 'Based on').next().find('a').first().should('have.attr', 'href', 'https://github.com/akhilrex/hammond')
    cy.contains('td', 'Support this fork').next().find('a').should('have.attr', 'href', 'https://paypal.me/nenadd26')
  })

  it('fill-up time: entered in the form, shown in the list, can be hidden in the settings', () => {
    cy.intercept('POST', `/api/vehicles/${vehicleId}/fillups`).as('createFillup')
    visit(`/vehicles/${vehicleId}/fillup`)
    // the form loads the last fill-up in the background (pre-fill, opens the station section): let it
    // finish before typing, else it re-renders mid-test and the save was occasionally blocked
    cy.contains('Last fill-up:')
    cy.contains('.field', 'Filling Station Name')
    cy.contains('.field', 'Quantity').find('input[type=number]').first().clear().type('9.5')
    cy.contains('.field', 'Price per').find('input').clear().type('1.7')
    cy.contains('.field', 'Odometer Reading').find('input').clear().type('21450')
    cy.get('.fillup-time input').clear().type('07:45')
    // name any field the browser considers invalid (it then silently refuses to submit)
    cy.get('form').first().then(($f) => {
      const bad = [...$f[0].elements].filter((e) => e.willValidate && !e.checkValidity())
        .map((e) => `${e.type} "${e.value}": ${e.validationMessage}`)
      expect(bad, 'invalid form fields').to.deep.eq([])
    })
    cy.get('form [type=submit]').first().click()
    cy.wait('@createFillup').its('request.body.date').then((iso) => {
      const d = new Date(iso)
      expect([d.getHours(), d.getMinutes()], 'saved with the entered time').to.deep.eq([7, 45])
    })
    visit(`/vehicles/${vehicleId}`)
    cy.contains('td', /[A-Z][a-z]{2} \d{2}, \d{4} 07:45/)
    cy.request({ method: 'POST', url: '/api/me/settings', headers: apiHeaders, body: { ...baseSettings, showFillupTime: false } })
    visit(`/vehicles/${vehicleId}`)
    cy.contains('td', '21450')
    cy.contains('td', '07:45').should('not.exist')
    cy.request({ method: 'POST', url: '/api/me/settings', headers: apiHeaders, body: { ...baseSettings, showFillupTime: true } })
  })

  it('stations nearby: admin sets the provider, the form lists E10 prices and fills one in', () => {
    // no profiles at all (the API smoke test set some up before Cypress runs: remove them): the free
    // sources (Austria, Slovenia, Croatia, France, Portugal) are built in, so the button is there anyway
    cy.request({ method: 'POST', url: '/api/settings/fuelPrices', headers: apiHeaders, body: { profiles: [] } })
    visit(`/vehicles/${vehicleId}/fillup`)
    cy.contains('button', 'Stations nearby').should('exist')
    cy.get('.station-setup-hint').should('not.exist')
    // fake Tankerkönig on 127.0.0.1:3999 (tests/fake_fuelprices.py, started by the workflow)
    cy.request({ method: 'POST', url: '/api/settings/fuelPrices', headers: apiHeaders,
      body: { profiles: [{ name: 'CI fake', kind: 'custom', url: 'http://127.0.0.1:3999/json', apiKey: 'ci-key' }] } })
    cy.request({ url: `/api/vehicles/${vehicleId}`, headers: apiHeaders }).then(({ body: v }) =>
      cy.request({ method: 'PUT', url: `/api/vehicles/${vehicleId}`, headers: apiHeaders, body: { ...v, fuelGrade: 'e10,e5' } }))

    visit('/admin/settings')
    cy.get('.price-profile').should('have.length', 1)
    cy.get('.price-profile select').should('have.value', 'custom')
    cy.get('.price-profile .share-profile input').should('be.checked') // shared with users by default
    cy.get('.price-profile input[type=password]').should('have.attr', 'placeholder', 'saved, hidden – type a new one to replace it').and('have.value', '')
    cy.document().its('documentElement.outerHTML').should('not.contain', 'ci-key')
    cy.get('.fuel-prices').contains('button', 'Test connection').click()
    cy.get('.test-result').should('contain', '✓ Works: 2 stations').and('contain', 'ARAL')

    // the phone is in Offenbach
    cy.visit(`/vehicles/${vehicleId}/fillup`, {
      onBeforeLoad(win) {
        cy.stub(win.navigator.geolocation, 'getCurrentPosition').callsFake((ok) => ok({ coords: { latitude: 50.1, longitude: 8.76 } }))
        win.navigator.share = cy.stub().resolves().as('share') // the phone's share sheet
      },
    })
    cy.contains('Last fill-up:')
    cy.intercept('GET', '/api/vehicles/*/stations*').as('stations')
    cy.contains('button', 'Stations nearby').click()
    cy.wait('@stations').its('request.url').should('contain', 'rad=2') // default radius (Settings)
    cy.get('.station-picker').within(() => {
      cy.contains('Stations nearby · 95 E10 / 95')
      cy.get('.price-profile-switch').should('exist') // own profile + the built-in free sources
      // both accepted grades per station; the station without E10 shows its E5 price; cheapest highlighted
      cy.contains('.station', 'ARAL').find('.price[data-grade=e10]').should('contain', '1.739')
      cy.contains('.station', 'ARAL').find('.price[data-grade=e5]').should('contain', '1.799')
      cy.contains('.station', 'Freie Tankstelle').find('.price[data-grade=e5]').should('contain', '1.769')
      cy.get('.price.cheapest').should('have.length', 1).and('contain', '1.739')
      // finger-sized Navigate / Share for stations with a position (the fake's ARAL has one)
      cy.contains('.station', 'ARAL').find('a.navigate').should('contain', 'Navigate').and('have.attr', 'href').and('contain', '50.1031,8.7601')
      cy.contains('.station', 'ARAL').find('button.share').click()
      cy.contains('.station', 'Freie Tankstelle').find('.station-actions').should('not.exist')
      cy.contains('.station', 'ARAL').find('.price[data-grade=e5]').click() // tanked E5 this time
    })
    cy.get('@share').should('have.been.calledWithMatch', { title: 'ARAL', url: 'https://www.google.com/maps/search/?api=1&query=50.1031,8.7601' })
    cy.contains('.field', 'Price per').find('input').should('have.value', '1.799')
    cy.contains('.field', 'Filling Station Name').find('input').should('have.value', 'ARAL, Frankfurter Str. 12, 63067 Offenbach')
    cy.contains('.field', 'Fuel Subtype').find('select').should('have.value', '95')
  })

  it('price profiles: admin adds and orders profiles, the form switches between them', () => {
    visit('/admin/settings')
    cy.get('.price-profile').should('have.length', 1) // CI fake from the test before
    cy.contains('.add-profile', 'Austria – E-Control').click()
    cy.get('.price-profile').eq(1).within(() => {
      cy.get('select').should('have.value', 'econtrol')
      cy.get('input[type=password]').should('not.exist') // Austria needs no key
      cy.get('input[type=url]').type('http://127.0.0.1:3999/nothing-here') // answers nothing useful
    })
    cy.get('.price-profile').eq(1).find('[title="Ask earlier"]').click()
    cy.get('.price-profile').eq(0).find('select').should('have.value', 'econtrol')
    cy.get('.fuel-prices input[type=submit]').click()
    cy.contains('Fuel price settings saved')
    // Austria is asked first, fails, the CI fake answers; the switch offers both
    cy.visit(`/vehicles/${vehicleId}/fillup`, {
      onBeforeLoad(win) {
        cy.stub(win.navigator.geolocation, 'getCurrentPosition').callsFake((ok) => ok({ coords: { latitude: 50.1, longitude: 8.76 } }))
      },
    })
    cy.contains('button', 'Stations nearby').click()
    cy.get('.station-picker .station').first().should('contain', 'ARAL')
    cy.get('.price-profile-switch select').find('option').should('have.length.at.least', 2) // + built-in free sources
    cy.get('.price-profile-switch select').should('contain', 'CI fake')
    cy.get('.station-picker').should('contain', 'CI fake ·')
    // back to one profile for the tests after this
    cy.request({ method: 'POST', url: '/api/settings/fuelPrices', headers: apiHeaders,
      body: { profiles: [{ name: 'CI fake', kind: 'custom', url: 'http://127.0.0.1:3999/json', apiKey: 'ci-key' }] } })
  })

  it('home assistant: a new fill-up is pre-filled with the car odometer', () => {
    // fake HA on 127.0.0.1:3999 (tests/fake_fuelprices.py); each user connects their own HA in Settings
    visit('/settings')
    cy.get('.my-price-profiles').should('contain', 'My fuel price profiles')
    cy.get('.station-radius input').should('have.value', '2')
    cy.get('.home-assistant input[type=url]').clear().type('http://127.0.0.1:3999')
    cy.get('.home-assistant input[type=password]').type('ci-ha-token')
    cy.get('.home-assistant button[type=submit]').click()
    cy.contains('Home Assistant settings saved')
    cy.get('.home-assistant input[type=password]').should('have.value', '').and('have.attr', 'placeholder', 'saved, hidden – type a new one to replace it')
    cy.document().its('documentElement.outerHTML').should('not.contain', 'ci-ha-token')
    cy.get('.home-assistant').contains('button', 'Test connection').click()
    cy.get('.ha-test-result').should('contain', '✓ Connected to CI Home (2026.11.1)')
    cy.request({ url: `/api/vehicles/${vehicleId}`, headers: apiHeaders }).then(({ body: v }) =>
      cy.request({ method: 'PUT', url: `/api/vehicles/${vehicleId}`, headers: apiHeaders, body: { ...v, odometerEntity: 'sensor.ci_odometer' } }))
    visit(`/vehicles/${vehicleId}/edit`)
    cy.contains('.field', 'Home Assistant odometer entity').find('input').should('have.value', 'sensor.ci_odometer')
    cy.get('.check-entity').click()
    cy.get('.entity-check').should('contain', '✓ Found: 23456 km')
    cy.contains('.field', 'Home Assistant odometer entity').find('input').clear().type('sensor.nope')
    cy.get('.check-entity').click()
    cy.get('.entity-check').should('contain', '✗ Home Assistant has no entity sensor.nope')

    visit(`/vehicles/${vehicleId}/fillup`)
    cy.contains('.field', 'Odometer Reading').find('input').should('have.value', '23456')
    cy.contains('.field', 'Odometer Reading').should('contain', 'From Home Assistant, updated').and('contain', 'Last fill-up:')
    noConsoleErrors()
    cy.request({ url: `/api/vehicles/${vehicleId}`, headers: apiHeaders }).then(({ body: v }) =>
      cy.request({ method: 'PUT', url: `/api/vehicles/${vehicleId}`, headers: apiHeaders, body: { ...v, odometerEntity: '' } }))
    cy.request({ method: 'POST', url: '/api/me/homeAssistant', headers: apiHeaders, body: { url: '', token: '-' } })
  })

  it('electric: a charge at a charging place is priced by its tariff', () => {
    cy.request({ method: 'POST', url: '/api/vehicles', headers: apiHeaders,
      body: { nickname: 'CY EV', registration: 'CY EV1', make: 'VW', model: 'ID.3', fuelUnit: 4, fuelType: 4 } }).its('body.id').then((evId) =>
      cy.request({ method: 'POST', url: '/api/chargingPlaces', headers: apiHeaders,
        body: { name: 'Home', lossPercent: 0, components: [{ name: 'Energy', price: 0.1,
          rules: [{ label: 'NT', days: 'all', from: '22:00', to: '06:00', price: 0.05 }] }] } }).its('body.id').then((placeId) => {
        visit('/charging')
        cy.contains('.charging-place', 'Home').click()
        cy.get('.tariff-rules tbody tr').should('have.length', 1)
        cy.get('.charging-place').should('not.contain', '&nbsp;') // spacer labels, not literal text
        cy.contains('button', 'Rule').click()
        cy.get('.tariff-rules tbody tr').should('have.length', 2) // not saved

        visit(`/vehicles/${evId}/fillup`)
        cy.get('.charging-place select').should('have.value', placeId) // the only place: preselected
        cy.contains('button', 'Stations nearby').should('not.exist')
        cy.get('.fillup-time input').clear().type('23:00')
        cy.get('.charge-end input').type('01:00')
        cy.contains('.field', 'Quantity').find('input').type('10')
        cy.get('.charge-quote').should('contain', 'Home: Energy · NT 0.50 = 0.50')
        cy.contains('.field', 'Price per').find('input').should('have.value', '0.05')
        cy.contains('.field', 'Filling Station Name').find('input').should('have.value', 'Home')
        // elsewhere: no quote, the price is typed
        cy.get('.charging-place select').select('')
        cy.get('.charge-quote').should('not.exist')
        noConsoleErrors()
        cy.request({ method: 'DELETE', url: `/api/chargingPlaces/${placeId}`, headers: apiHeaders })
        cy.request({ method: 'DELETE', url: `/api/vehicles/${evId}`, headers: apiHeaders })
      }))
  })

  it('detected fill-up with a location: the form lists stations near it right away', () => {
    cy.request({ method: 'POST', url: '/api/me/apikeys', headers: apiHeaders, body: { name: 'location test' } }).then(({ body: k }) =>
      cy.request({ method: 'POST', url: `/api/v1/vehicles/${vehicleId}/detections`, headers: { 'X-Api-Key': k.key },
        body: { odoReading: 22100, fuelQuantity: 30, lat: 50.1, lng: 8.76, date: '2026-11-07T16:00:00Z' } }))
      .its('body.detection.id').then((id) => {
        visit(`/vehicles/${vehicleId}/fillup?detection=${id}`)
        cy.get('.station-picker').contains('Stations near the detected location · 95 E10 / 95')
        cy.get('.station-picker .station').first().should('contain', 'ARAL')
        cy.request({ method: 'POST', url: `/api/detections/${id}/discard`, headers: apiHeaders })
      })
  })

  it('detected fill-up at a station: that station and its live price are taken', () => {
    cy.request({ method: 'POST', url: '/api/me/apikeys', headers: apiHeaders, body: { name: 'at station test' } }).then(({ body: k }) =>
      cy.request({ method: 'POST', url: `/api/v1/vehicles/${vehicleId}/detections`, headers: { 'X-Api-Key': k.key },
        body: { odoReading: 22100, fuelQuantity: 30, lat: 50.1031, lng: 8.7601, date: '2026-11-07T16:00:00Z' } }))
      .its('body.detection.id').then((id) => {
        visit(`/vehicles/${vehicleId}/fillup?detection=${id}`)
        cy.contains('.field', 'Filling Station Name').find('input').should('have.value', 'ARAL, Frankfurter Str. 12, 63067 Offenbach')
        cy.contains('.field', 'Price per').find('input').invoke('val').should('match', /^1\.(739|799|629)$/) // ARAL's price of the vehicle's grade
        cy.request({ method: 'POST', url: `/api/detections/${id}/discard`, headers: apiHeaders })
      })
  })

  it('fill-up form opened by a fresh page load can be saved while the vehicles are still loading', () => {
    // upstream copied the vehicle list once at mount: slow loading left the required vehicle select empty,
    // so the browser silently refused to save (seen in CI as a "flaky" test)
    cy.intercept('GET', '/api/me/vehicles', (req) => req.on('response', (res) => res.setDelay(3000))).as('vehicles')
    cy.intercept('POST', `/api/vehicles/${vehicleId}/fillups`).as('save')
    visit(`/vehicles/${vehicleId}/fillup`)
    cy.contains('.field', 'Select a vehicle').find('select').invoke('val').should('not.be.empty')
    cy.contains('.field', 'Quantity').find('input[type=number]').first().clear().type('5')
    cy.contains('.field', 'Price per').find('input').clear().type('1.8')
    cy.contains('.field', 'Odometer Reading').find('input').clear().type('22300')
    cy.get('form [type=submit]').first().click()
    cy.wait('@save').its('response.statusCode').should('eq', 201)
  })

  it('fuel subtype is a drop-down for the vehicle\'s fuel type, defaulting to its grade', () => {
    visit(`/vehicles/${vehicleId}/fillup`)
    cy.contains('Last fill-up:')
    cy.contains('.field', 'Fuel Subtype').find('select option').then(($o) => {
      const values = [...$o].map((o) => o.value)
      expect(values).to.include.members(['', 'Diesel', 'Premium Diesel', 'HVO100']) // the test vehicle is diesel
      expect(values).to.not.include('95')
    })
    cy.contains('.field', 'Fuel Subtype').find('select option').contains('Eurodizel')
    cy.request({ method: 'POST', url: '/api/me/settings', headers: apiHeaders, body: { ...baseSettings, customFuelSubtypes: 'Q Max 95\nMaxxMotion 100' } })
    visit(`/vehicles/${vehicleId}/fillup`)
    cy.contains('.field', 'Fuel Subtype').find('select').select('Q Max 95').should('have.value', 'Q Max 95')
    cy.contains('.field', 'Fuel Subtype').find('select option').contains('MaxxMotion 100')
  })

  it('vehicle edit shows the gauge calibration (segments, status, litres per segment)', () => {
    cy.request({ url: `/api/vehicles/${vehicleId}/gauge`, headers: apiHeaders }).then(({ body: g }) => {
      expect(g.segments).to.eq(4)
      expect(g.segmentLitres).to.have.length(4)
    })
    visit(`/vehicles/${vehicleId}/edit`)
    cy.contains('.field', 'Gauge segments').find('input').should('have.value', '4')
    cy.get('.gauge-calibration').should('contain', 'Gauge calibration').find('.gauge-segment').should('have.length', 4)
    cy.get('.gauge-calibration').contains(/linear|learned from/)
    // by hand: litres per bar, saved, shown as "set by hand"; back to learned
    cy.get('.gauge-calibration .edit-gauge').click()
    ;[8, 10, 11, 14].forEach((l, i) => cy.get('.gauge-calibration .gauge-edit input').eq(i).clear().type(String(l)))
    cy.intercept('PUT', `/api/vehicles/${vehicleId}/gauge/manual`).as('manual')
    cy.get('.gauge-calibration .save-gauge').click()
    cy.wait('@manual').its('response.body.segmentLitres').should('deep.eq', [8, 10, 11, 14])
    cy.get('.gauge-calibration').should('contain', 'set by hand').and('contain', 'Sum 43')
    cy.get('.gauge-calibration .use-learned').click()
    cy.wait('@manual').its('response.body.manual').should('eq', false)
    cy.get('.gauge-calibration').should('not.contain', 'set by hand')
    noConsoleErrors()
  })

  it('price watch: watch a station from Stations nearby, set a limit, show the graph', () => {
    // a fixed position (the fake price service answers anywhere)
    cy.visit('/prices', {
      onBeforeLoad: (win) => {
        cy.spy(win.console, 'error').as('consoleError')
        cy.stub(win.navigator.geolocation, 'getCurrentPosition').callsFake((ok) => ok({ coords: { latitude: 50.1, longitude: 8.76 } }))
      },
    })
    cy.get('.no-watches').should('exist')
    // without the device's location: tap the map
    cy.get('.add-stations .show-map').click()
    cy.get('.add-stations .station-map').should('be.visible').click()
    cy.get('.add-stations .station').should('have.length.at.least', 1)
    cy.get('.add-stations .station-map .leaflet-interactive').should('exist') // price markers
    cy.get('.add-stations .stations-nearby').click()
    cy.get('.add-stations .station').first().find('.watch').click()
    cy.get('.add-stations .station').first().find('.watch-grade:visible').first().click()
    cy.get('.price-watch').should('have.length', 1).and('contain', 'ARAL')
    cy.get('.price-watch .last-price').invoke('text').should('match', /\d\.\d{3}/)
    // a second grade of the same station: same card, own row; the first one is ticked in the menu
    cy.get('.add-stations .station').first().find('.watch').click()
    cy.get('.add-stations .station').first().find('.watch-grade.is-disabled:visible').should('have.length', 1).and('contain', '✓')
    cy.get('.add-stations .station').first().find('.watch-grade[data-grade="diesel"]:visible').click()
    cy.get('.price-watch').should('have.length', 1).find('.watch-grade-row').should('have.length', 2)
    cy.get('.price-watch .watch-grade-row[data-grade="diesel"] .delete-watch').click()
    cy.get('.modal .button.is-danger').click()
    cy.get('.price-watch .watch-grade-row').should('have.length', 1)
    cy.intercept('PUT', '/api/me/priceWatches/*').as('saveWatch')
    cy.get('.price-watch .below input').type('1.70')
    cy.get('.price-watch .save-watch').click()
    cy.wait('@saveWatch').its('response.statusCode').should('eq', 200)
    cy.intercept('GET', '/api/me/priceWatches/*/history*').as('history')
    cy.get('.price-watch .show-graph').click()
    cy.wait('@history')
    cy.get('.price-watch canvas').should('be.visible')
    cy.get('.price-watch-settings').should('contain', 'notification channels')
    noConsoleErrors()
    cy.request({ url: '/api/me/priceWatches', headers: apiHeaders }).then(({ body }) =>
      body.forEach((w) => cy.request({ method: 'DELETE', url: `/api/me/priceWatches/${w.id}`, headers: apiHeaders })))
  })

  it('trips: a trip entered by hand shows per day; purpose and a toll can be added; fits a phone', () => {
    const start = new Date(Date.now() - 3 * 3600e3)
    cy.request({ method: 'POST', url: `/api/vehicles/${vehicleId}/trips`, headers: apiHeaders,
      body: { start: start.toISOString(), end: new Date(start.getTime() + 45 * 60e3).toISOString(),
              startOdo: 5000, endOdo: 5042, purpose: '', business: true } })
    cy.viewport(390, 844)
    visit(`/vehicles/${vehicleId}/trips`)
    cy.get('.trips-page .trip', { timeout: 10000 }).should('have.length', 1).and('contain', '42.0').and('contain', '45 min')
    cy.intercept('PUT', '/api/trips/*').as('saveTrip')
    cy.get('.trips-page .trip input[placeholder^="Purpose"]').type('Customer A')
    cy.get('.trips-page .trip').contains('button', 'Save').click()
    cy.wait('@saveTrip').its('response.statusCode').should('eq', 200)
    cy.intercept('POST', '/api/trips/*/costs').as('addCost')
    cy.get('.trips-page .trip').contains('button', 'Add cost').click()
    cy.get('.trips-page .trip input[type=number]').type('9.90')
    cy.get('.trips-page .trip').contains('button', /^\s*Add\s*$/).click()
    cy.wait('@addCost').its('response.statusCode').should('eq', 201)
    cy.get('.trips-page .trip').should('contain', 'Toll').and('contain', '9.90')
    // the map: a trip entered by hand has no route, so the map says how to get one
    cy.get('.trips-page .trips-map .leaflet-container').should('exist')
    cy.get('.trips-page .trips-map').should('contain', 'No routes yet')
    cy.document().then((doc) => expect(doc.documentElement.scrollWidth).to.be.at.most(390))
    noConsoleErrors()
  })

  it('car photo: chosen on the vehicle page, shown on the home card, can be hidden (only for Home Assistant)', () => {
    const png = Cypress.Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==', 'base64')
    visit(`/vehicles/${vehicleId}/edit`)
    cy.intercept('PUT', `/api/vehicles/${vehicleId}/photo`).as('photo')
    cy.get('.vehicle-photo input.photo-input', { timeout: 10000 }).selectFile({ contents: png, fileName: 'car.png', mimeType: 'image/png' }, { force: true })
    cy.wait('@photo').its('response.statusCode').should('eq', 200)
    cy.get('.vehicle-photo img').should('have.attr', 'src').and('match', /^\/api\/public\/photo\/[0-9a-f]{32}$/)
    visit('/')
    cy.get('img.car-thumb', { timeout: 10000 }).should('exist')
    visit(`/vehicles/${vehicleId}/edit`)
    cy.intercept('PUT', `/api/vehicles/${vehicleId}/photoHidden`).as('hide')
    cy.get('.vehicle-photo .photo-shown', { timeout: 10000 }).click()
    cy.wait('@hide').its('request.body.hidden').should('eq', true)
    visit('/')
    cy.contains('.card-header-title', 'Cypress', { timeout: 10000 })
    cy.get('img.car-thumb').should('not.exist')
    noConsoleErrors()
  })

  it('trips: the Trips menu opens the trips of a car (the one picked last time)', () => {
    visit(`/vehicles/${vehicleId}/trips`)
    cy.get('.trips-page', { timeout: 10000 })
    visit('/trips')
    cy.location('pathname', { timeout: 10000 }).should('eq', `/vehicles/${vehicleId}/trips`)
    // desktop width: date fields (with a label) and the buttons in one row, on one bottom line
    // (on a phone they stack, so this is only checked here)
    cy.get('.trips-page .trips-toolbar input[type=date]', { timeout: 10000 }).first().then(($d) => {
      cy.get('.trips-page .trips-toolbar .buttons .button').first().then(($b) => {
        expect(Math.abs($d[0].getBoundingClientRect().bottom - $b[0].getBoundingClientRect().bottom)).to.be.lessThan(3)
      })
    })
    noConsoleErrors()
  })

  it('notifications: add an ntfy channel, turn it off, delete it', () => {
    cy.viewport(390, 844)
    visit('/settings')
    cy.get('.notifications .add-ntfy', { timeout: 10000 }).click()
    cy.get('.notifications .channel-form input[type=url]').type('https://ntfy.example.com/cypress-topic')
    cy.intercept('POST', '/api/me/notifications').as('addChannel')
    cy.get('.notifications .save-channel').click()
    cy.wait('@addChannel').its('response.statusCode').should('eq', 201)
    cy.get('.notifications .channel[data-kind=ntfy]').should('contain', 'cypress-topic')
    cy.intercept('PUT', '/api/me/notifications/*').as('toggle')
    cy.get('.notifications .channel[data-kind=ntfy] .channel-enabled').click()
    cy.wait('@toggle').its('response.body.enabled').should('eq', false)
    cy.get('.notifications .channel[data-kind=ntfy] .delete-channel').click()
    cy.get('.modal .button.is-danger').click()
    cy.get('.notifications .channel').should('have.length', 0)
    cy.document().then((doc) => expect(doc.documentElement.scrollWidth).to.be.at.most(390))
    noConsoleErrors()
  })

  it('logs out, and a wrong password is refused', () => {
    // log out through the menu like a user: opening /logout directly redirects twice (/logout → / → /login),
    // and typing into the first login page raced the second (flaky in CI)
    cy.contains('.navbar a', 'Log out').click()
    cy.location('pathname').should('eq', '/login')
    cy.window().its('localStorage').invoke('getItem', 'auth.currentUser').should('eq', 'null')
    cy.intercept('POST', '/api/login').as('login')
    fillLogin('wrong-password')
    cy.wait('@login').its('response.statusCode').should('eq', 403)
    cy.contains('There was an error logging in')
    cy.location('pathname').should('eq', '/login')
  })
})
