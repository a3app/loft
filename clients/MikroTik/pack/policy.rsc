# loft — LAN through the container as gateway.
# Run only after the container log shows "tun up". Without TUN this loops.
# Edit the address-list, then paste.

:if ([:len [/routing/table/find where name="loft"]] = 0) do={
  /routing/table/add fib name=loft
}

:if ([:len [/ip/route/find where routing-table="loft" and dst-address="0.0.0.0/0"]] = 0) do={
  /ip/route/add dst-address=0.0.0.0/0 gateway=172.31.20.2 routing-table=loft
}

:if ([:len [/ip/firewall/address-list/find where list="loft-lan"]] = 0) do={
  /ip/firewall/address-list/add list=loft-lan address=192.168.88.0/24 comment="LAN that uses loft — edit"
}

:if ([:len [/ip/firewall/mangle/find where comment="loft-lan"]] = 0) do={
  /ip/firewall/mangle/add chain=prerouting src-address-list=loft-lan \
    dst-address=!172.31.20.0/24 dst-address-type=!local \
    action=mark-routing new-routing-mark=loft passthrough=no comment=loft-lan
} else={
  # A rule from an older policy.rsc lacks the !local match; add it in place.
  /ip/firewall/mangle/set [find where comment="loft-lan"] dst-address-type=!local
}
# dst-address-type=!local: packets to the router's own addresses (its DNS,
# WebFig, Winbox) stay on the router. Table loft has only a default route,
# so without this they would be sent into the container and lost.

:put "hosts in address-list loft-lan now use table loft → 172.31.20.2"
:put "fronts stay on veth-loft inside the container (-if 172.31.20.1); they do not hairpin"
