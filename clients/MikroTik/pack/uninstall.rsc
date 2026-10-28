# loft — remove container, veth, routes. YAML on disk stays.

:do { /container/stop loft } on-error={}
:delay 2s
:do { /container/remove loft } on-error={}
:do { /container/mounts/remove [find where list="loft-data"] } on-error={}
:do { /ip/firewall/mangle/remove [find where comment="loft-lan"] } on-error={}
:do { /ip/firewall/address-list/remove [find where list="loft-lan"] } on-error={}
:do { /ip/route/remove [find where routing-table="loft"] } on-error={}
:do { /routing/table/remove [find where name="loft"] } on-error={}
:do { /routing/rule/remove [find where comment="loft-wan"] } on-error={}
:do { /ip/route/remove [find where comment="loft-wan"] } on-error={}
:do { /routing/table/remove [find where name="loft-wan"] } on-error={}
:do { /ip/firewall/nat/remove [find where comment="loft-container"] } on-error={}
:do { /interface/bridge/port/remove [find where interface="veth-loft"] } on-error={}
:do { /ip/address/remove [find where interface="br-loft"] } on-error={}
:do { /interface/veth/remove [find where name="veth-loft"] } on-error={}
:do { /interface/bridge/remove [find where name="br-loft"] } on-error={}
# install.rsc pointed the container tmpdir at <disk>/loft/tmp. Clear it only
# if it is still ours, so another container setup is left alone.
:do {
  :if ([/container/config/get tmpdir] ~ "/loft/tmp\$") do={ /container/config/set tmpdir="" }
} on-error={}
:put "loft container and routes removed; disk files <disk>/loft/ kept"
