# Telegram bot

A node started with `telegraph sync --serve` and a bot token
(`TELEGRAPH_BOTS`) gossips on its configured channels and also answers
people who message the bot. Replies come from the node's local store.
Text a person sends is never parsed as gossip; the only entries the bot
writes are `/register` and `/revoke`, signed by the node itself.

## Commands

The command menu (the **/** button in Telegram) and `/help` are built from
one table in `internal/transport/dm.go`, so they always match.

| Command | Reply |
|---|---|
| `/register name [did]` | Registers `name@nym`, signed by this node (see below). Without a name, the bot asks for one. Private chats only. |
| `/card [name]` | Your bio line `⟁ name@nym · 1a2b3c4d` in a code block, so one tap copies it. The node signs a card attestation the first time, so `telegraph scan` reports it VERIFIED. Lists every handle you hold here; with no handle it says how to get one. |
| `/resolve handle@nym` | Owner key, DID, status and whether a checkpoint attests it. Without a handle, the bot asks for one and resolves your next message. |
| `/revoke name` | Releases a handle you hold through the bot. Private chats only. |
| `/status` | Entries, handles and issuers; latest checkpoint, its age and committee signatures; audit result |
| `/stats` | Network counts: active, revoked and total handles, registrars, nodes online, swaps, checkpoints, your handles here, and early-adopter spots left. `/register` shows your handle's number and a badge (Founding 100, Early adopter for the first 1000) |
| `/pin` | The network's trust pin (a checkpoint fingerprint, safe to share) |
| `/about` | What Telegraph is and how it works |
| `/version` | Node software version, with a Releases button |
| `/help` | Every command with a one-line description |
| `/start` | Welcome, with Channel, GitHub, Wiki and Releases buttons. The Channel button is the public link (`TELEGRAPH_CHANNEL_URL`, default `https://t.me/tgraph_net`), never an invite link. |

Without `--bot-register`, `/register`, `/revoke` and `/card` are left out
of the menu and `/help`.

A message that is just a handle (`alice@nym`) is resolved. Other text gets
a short pointer to `/help`; unknown commands say so.

## Registering from Telegram

Off by default. A node operator turns it on with
`telegraph sync --serve --bot-register` (or `TELEGRAPH_BOT_REGISTER=1`);
otherwise `/register` says the node does not register handles.

- **Custodial.** The node signs the register entry with its own key and
  gossips it, so the handle resolves everywhere and `/resolve` shows the
  node's key as owner. The node stores the Telegram user id for each
  handle in `custody` in its `data.json` (local, never gossiped) and lets
  only that user change the DID or `/revoke`. Since 0.3.0
  ([#15](https://github.com/ao3575911/telegraph/issues/15)) ownership is
  first-come across the network: no other key can take the handle over,
  and the bot never signs for a handle another key owns. Custody records
  for handles that were revoked or swapped away are dropped. To hold the
  key yourself, run a node and use `telegraph register`.
- **Names.** `adz`, `Adz` and `adz@nym` all mean `adz@nym`. Names are 1–32
  characters of `a-z 0-9 _ . -`, not starting with `_ . -`; only `@nym`.
- **DID.** `did:method:id`, at most 200 characters. Without one the handle
  gets `did:tg:<telegram user id>`: a label tying it to your Telegram
  account, not a resolvable DID, and public in the log.
- **Refused:** handles owned by another key, handles the node's operator
  registered, handles another Telegram user holds, handles locked in a
  swap, a fourth handle per Telegram user, and any `/register` or
  `/revoke` in a group (the bot points you to a private chat).
- Sending `/register` again with a new DID updates it. A revoked handle is
  free for anyone to register.

## Where it answers

- **Direct messages:** every command above.
- **Groups:** only commands addressed to the bot (`/status@botname`),
  as a reply to the command. Everything else in a group is ignored, and a
  group can never trigger a log dump or feed entries to the node.
- **Gossip channels** (`TELEGRAPH_CHANNELS`): entries and `/dump`
  requests between nodes. `/dump` is a node protocol message and is not in
  the menu.
- Edited messages are not requested and are ignored.

## Limits and formatting

- 5 replies per chat per 30 seconds. The first message over the limit gets
  "⏳ Too many messages. Try again in N s."; later ones are dropped until
  the window clears.
- Errors read alike everywhere: "⚠️ Usage: …" for malformed commands, "⚠️ …
  is not registered" for unknown handles, and "⚠️ Something went wrong on
  this node. Please try again in a minute." for internal failures, which
  are logged for the operator. No error text, stack trace, path or key
  material reaches a chat; a panic in a command is caught and logged.
- Replies use Telegram HTML; anything a person typed is escaped and long
  input is clipped. Link previews are off.

## Node setup

On start a serving node calls `getMe` (to recognise commands addressed to
it in groups), `setMyCommands` for the default and private-chat scopes, and
`setChatMenuButton` to show the command menu, and sets the bot's
description and short description (`setMyDescription`,
`setMyShortDescription`) with the current version, skipping either when
`getMyDescription`/`getMyShortDescription` already match.

Every `getUpdates` call sends
`allowed_updates=["message","channel_post","my_chat_member"]`: Telegram
keeps the last value a client sent, and a stale filter can silently drop
direct messages. Polls use a 20 s long-poll over HTTP/1.1 with a 35 s
request deadline; after a failure the node drops idle connections and backs
off (1 s doubling to 30 s, with jitter, or Telegram's `retry_after`).
Repeated identical errors are logged at most once a minute, and the bot
token is never written to logs. SIGTERM or SIGINT aborts the long poll,
saves the store and exits 0. Running a node as a service:
[NODE-OPERATIONS.md](NODE-OPERATIONS.md).
