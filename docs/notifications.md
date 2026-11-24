# Notifications and chat

*Settings → Notifications → Add*: as many channels as you like, each on/off, with **Test**. Price alerts go to
every channel that is on. Telegram and Matrix can also be a **chat bot** for your fuel data.

## Gotify

1. In Gotify: *Apps → Create application* → copy its **token**.
2. In hammond: *Add → Gotify*, **Server URL** `https://gotify.example.com`, **App token**, *Save*, *Test*.

## ntfy

1. Pick a topic name nobody guesses, e.g. `hammond-7f3k2`. In the ntfy app: *Subscribe* to it.
2. In hammond: *Add → ntfy*, **Topic URL** `https://ntfy.sh/hammond-7f3k2` (or your own server).
   A protected topic needs its **access token** (`tk_…`).

## Telegram (alerts and chat bot)

1. In Telegram: open **@BotFather** → `/newbot` → a name → copy the **token** (`123456789:AA…`).
2. In hammond: *Add → Telegram*, paste the **Bot token**, turn on *Answer the fuel commands in the linked chat*,
   *Save*. hammond shows a code.
3. In Telegram: open your new bot → send `/start <code>`. Only that chat is answered.

hammond asks Telegram for new messages itself: no webhook, no open port.

## Matrix

**Alerts only:** *Add → Matrix*, **Homeserver URL**, the **Room** (ID `!abc…` or alias `#fuel:example.com`, or a
matrix.to link) and the **access token** of the account that posts.

**As a chat bot** (like Telegram):

1. Create a bot account, e.g. `@hammond:example.com` (Synapse: register a user; Conduit/Continuwuity admin room:
   `!admin users create-user hammond`).
2. Make an **unencrypted** room and **invite the bot**.
3. In hammond: *Add → Matrix*, homeserver and room; *Get the token from the bot's username and password* fills in
   the token (the password isn't saved). Turn on *Answer the fuel commands in this room*, *Save*, *Test* (hammond
   joins the room).
4. In the room: send `/start <code>` (shown in the settings). Other people in the room are ignored.

## Chat commands

`prices`, `cheap` (cheapest hours), `graph fuel 30`, `stats`, `trips 10 pdf`, `fill 40.5 1.759`, `cost toll 12.50 A3`, `undo`, a receipt
photo → Quick Entry, and answering "fill-up detected, price?" with just the price. The full list:
[main README → Chat commands](../README.md#chat-commands-telegram-and-matrix).
