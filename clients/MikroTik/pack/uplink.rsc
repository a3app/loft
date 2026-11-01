# loft — pin container egress to a RouterOS WAN.
# Edit $wan (ether1, lte1, pppoe-out1, wg1). Empty = default table.
# The GUI writes this file next to client.yaml after Save.

:local wan "ether1"
:local src "172.31.20.0/24"

:if ([:len [/routing/table/find where name="loft-wan"]] = 0) do={
  /routing/table/add fib name=loft-wan
}
:do { /ip/route/remove [find where comment="loft-wan"] } on-error={}
:do { /routing/rule/remove [find where comment="loft-wan"] } on-error={}
:if ($wan != "") do={
  /ip/route/add dst-address=0.0.0.0/0 gateway=$wan routing-table=loft-wan comment=loft-wan
  /routing/rule/add src-address=$src action=lookup-only-in-table table=loft-wan comment=loft-wan
}
:put ("loft container 172.31.20.0/24 egress → " . $wan)
