---
title: Telegraph
---

<img src="banner.svg" alt="Telegraph" width="100%">

Telegraph is a decentralised handle registry: signed, hash-chained logs
gossiped over Telegram channels and attested by rotating 2-of-3 checkpoint
committees. One static Go binary, standard library only.

<img src="demo/main.gif" alt="Three nodes register, gossip, checkpoint and verify" width="100%">

```sh
go install github.com/ao3575911/telegraph/cmd/telegraph@latest
```

- [Quickstart](QUICKSTART.html): a three-node run on one machine
- [Architecture](https://github.com/ao3575911/telegraph/wiki/Architecture) · [CLI reference](https://github.com/ao3575911/telegraph/wiki/CLI-reference) · [FAQ](https://github.com/ao3575911/telegraph/wiki/FAQ)
- [Usage report](USAGE-REPORT.html): a measured run of every command
- Recordings: [help](demo/help.gif) · [sync](demo/sync.gif) ·
  [checkpoint](demo/checkpoint.gif) · [card](demo/card.gif) · [swap](demo/swap.gif)
- [Source](https://github.com/ao3575911/telegraph) ·
  [Releases](https://github.com/ao3575911/telegraph/releases) ·
  [Changelog](https://github.com/ao3575911/telegraph/blob/main/CHANGELOG.md)
