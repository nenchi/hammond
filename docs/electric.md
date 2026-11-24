# Electric and EVCC

For electric cars and plug-in hybrids: kWh instead of litres, priced by where you charged.

## Charging places

*Charging places*: home, work, a public charger.

- **Tariff parts** with their price per kWh and rules (months; every day, working days, or weekend + holidays, with
  the place's own holidays; from–to time), e.g. a cheaper night rate; a **session fee**; **charging loss %** (added to the kWh you enter, what the car got).
- **Home Assistant price sensor** (optional): a dynamic tariff's price per kWh over time, added to the parts.
- **Where** and a **radius**: a detected charge within that distance picks this place.
- Admins can **share** a place with all users (they use it, only admins change it).

A new charge (fill-up of an electric vehicle) at a place is priced by its tariff from the start and end time.

## EVCC

*Settings → EVCC*: your EVCC URL, **Sync every (minutes)** (0 = only *Sync now*), **Import sessions from** (empty at
the first save = the last 7 days). *Edit vehicle* → the vehicle's name in EVCC (its title): each finished EVCC
charging session of that vehicle becomes a **detected charge** (menu *Detected*) that you confirm, priced with your
charging place's tariff, else with EVCC's own price. EVCC's `/api/sessions` needs no login.
