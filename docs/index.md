---
title: Telegraph
---

<img src="banner.svg" alt="Telegraph" width="100%">

Telegraph is a decentralised handle registry: signed, hash-chained logs
gossiped over Telegram channels and attested by rotating 2-of-3 checkpoint
committees. One static Go binary, standard library only.

<img src="demo/main.gif" alt="Three nodes register, gossip, checkpoint and verify" width="100%">

```sh
curl -fsSL https://raw.githubusercontent.com/ao3575911/telegraph/main/scripts/install.sh | sh  # no Go needed
go install github.com/ao3575911/telegraph/cmd/telegraph@v0.2.2                               # or with Go 1.22+
```

On Telegram, DM a node's bot: `/resolve alice@nym` to look up a handle, or
`/register adz` to claim `adz@nym`. Bot registration is custodial: the
node's key owns the handle, and only your Telegram account can change or
revoke it there. See [Telegram bot](TELEGRAM.html).

- [Quickstart](QUICKSTART.html): a three-node run on one machine, and how to register a handle
- [Telegram bot](TELEGRAM.html): commands, custody rules, limits
- [Architecture](https://github.com/ao3575911/telegraph/wiki/Architecture) · [CLI reference](https://github.com/ao3575911/telegraph/wiki/CLI-reference) · [FAQ](https://github.com/ao3575911/telegraph/wiki/FAQ)
- [Usage report](USAGE-REPORT.html): a measured run of every command
- Recordings: [help](demo/help.gif) · [sync](demo/sync.gif) ·
  [checkpoint](demo/checkpoint.gif) · [card](demo/card.gif) · [swap](demo/swap.gif)
- [Source](https://github.com/ao3575911/telegraph) ·
  [Releases](https://github.com/ao3575911/telegraph/releases) ·
  [Changelog](https://github.com/ao3575911/telegraph/blob/main/CHANGELOG.md)
