# Pinned post draft for @tgraph_net

Draft only: an operator posts and pins it by hand after v0.3.0 is tagged
and the live node runs it. Nodes ignore non-frame posts, so it is safe in
the gossip channel. Telegram formatting: paste as is (bold and code come
from the Telegram editor, or send with parse_mode HTML using the second
version).

## Plain text

```text
⟁ Telegraph v0.3.0 — decentralised handles on Telegram

Claim a handle like adz@nym. It lives in signed, hash-chained logs that
nodes gossip here and checkpoint together (2-of-3 committee). First come,
first served: only the owner can change, revoke or swap a handle.

Get yours
1. Open @dsmdioefncoedcbot and send /register yourname
2. Send /card and tap the line to copy it
3. Paste it into your Telegram bio, e.g. ⟁ adz@nym · 1a2b3c4d

Look anyone up: /resolve name@nym
First 100 handles get 🏅 Founding 100, the first 1000 🎖 Early adopter (/stats)

The TGP1|… messages below are node-to-node gossip frames; you can ignore them.

Run your own node: https://github.com/ao3575911/telegraph
Docs: https://github.com/ao3575911/telegraph/wiki
```

## HTML (for sendMessage with parse_mode HTML)

```html
<b>⟁ Telegraph v0.3.0</b> — decentralised handles on Telegram

Claim a handle like <code>adz@nym</code>. It lives in signed, hash-chained logs that nodes gossip here and checkpoint together (2-of-3 committee). First come, first served: only the owner can change, revoke or swap a handle.

<b>Get yours</b>
1. Open @dsmdioefncoedcbot and send <code>/register yourname</code>
2. Send <code>/card</code> and tap the line to copy it
3. Paste it into your Telegram bio, e.g. <code>⟁ adz@nym · 1a2b3c4d</code>

Look anyone up: <code>/resolve name@nym</code>
First 100 handles get 🏅 Founding 100, the first 1000 🎖 Early adopter (/stats)

The <code>TGP1|…</code> messages below are node-to-node gossip frames; you can ignore them.

<a href="https://github.com/ao3575911/telegraph">Run your own node</a> · <a href="https://github.com/ao3575911/telegraph/wiki">Docs</a>
```
