# Telegram bot

A node started with `telegraph sync --serve` and a bot token
(`TELEGRAPH_BOTS`) gossips on its configured channels and also answers
people who message the bot. Replies come from the node's local store and
are read-only: nothing a person sends can enter the gossip log.

## Commands

The command menu (the **/** button in Telegram) and `/help` are built from
one table in `internal/transport/dm.go`, so they always match.

| Command | Reply |
|---|---|
| `/resolve handle@nym` | Owner key, DID, status and whether a checkpoint attests it. Without a handle, the bot asks for one and resolves your next message. |
| `/status` | Entries, handles and issuers; latest checkpoint, its age and committee signatures; audit result |
| `/pin` | The network's trust pin (a checkpoint fingerprint, safe to share) |
| `/about` | What Telegraph is and how it works |
| `/version` | Node software version, with a Releases button |
| `/help` | Every command with a one-line description |
| `/start` | Welcome, with GitHub, Wiki, Channel and Release buttons |

A message that is just a handle (`alice@nym`) is resolved. Other text gets
a short pointer to `/help`; unknown commands say so.

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
  "Too many messages. Try again in N s."; later ones are dropped until the
  window clears.
- Replies use Telegram HTML; anything a person typed is escaped and long
  input is clipped. Link previews are off.

## Node setup

On start a serving node calls `getMe` (to recognise commands addressed to
it in groups), `setMyCommands` for the default and private-chat scopes, and
`setChatMenuButton` to show the command menu.

Every `getUpdates` call sends
`allowed_updates=["message","channel_post","my_chat_member"]`: Telegram
keeps the last value a client sent, and a stale filter can silently drop
direct messages. Polls use a 20 s long-poll over HTTP/1.1 with a 35 s
request deadline; after a failure the node drops idle connections and backs
off (1 s doubling to 30 s, with jitter, or Telegram's `retry_after`).
Repeated identical errors are logged at most once a minute, and the bot
token is never written to logs.
