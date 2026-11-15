# Run a node

A serving node gossips on the channel, co-signs checkpoints and answers its
bot. Run it as a user service so it restarts on failure and after a reboot
(no sudo):

```sh
sh scripts/install-service.sh node1     # writes ~/.config/telegraph/node1.env (0600)
$EDITOR ~/.config/telegraph/node1.env   # paste the bot token
sh scripts/install-service.sh node1     # installs and starts telegraph@node1
systemctl --user status telegraph@node1
journalctl --user -u telegraph@node1 -f
```

macOS gets a launchd agent from the same script; hosts without systemd use
`scripts/telegraph-supervise.sh` from cron `@reboot`.

To add a committee node: create its own bot, add it to
[@tgraph_net](https://t.me/tgraph_net) as an admin, then
set `TELEGRAPH_COMMITTEE` in its env file to the network's current list
(required: a node will not sync over Telegram without it), then run
`sh scripts/node-join.sh node2 <pin> node2`. It bootstraps from the pin,
registers `node2@nym` so the node key becomes an issuer (only issuers can
sit on the checkpoint committee), and installs the service. Add its key
(`telegraph config key`) to `TELEGRAPH_PEERS` on the other nodes and to
`TELEGRAPH_COMMITTEE` on every node (the committee allowlist, identical
everywhere), then restart them together.

Settings, failure behaviour, the committee rules and the full runbook:
[NODE-OPERATIONS.md](https://github.com/ao3575911/telegraph/blob/main/docs/NODE-OPERATIONS.md).
