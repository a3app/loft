# loft — LAN through the container as gateway.
# Run only after the container log shows "tun up". Without TUN this loops.
# Edit the address-list and $mode, then paste.
#
# $mode must match listen.gateway in client.yaml:
#   nat   (default) RouterOS rewrites LAN sources to 172.31.20.1 (masquerade)
#         on the way into the container. loft needs nothing extra.
#   route LAN addresses stay as they are; loft (listen.gateway: route) sends
#         its answers back here by a policy rule. Use it when the container
#         should see real LAN addresses.
:local mode "nat"

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

# Why nat at all: inside the container loft routes 0/1 and 128/1 into its
# TUN. An answer to a LAN address would follow those routes back into the
# TUN instead of returning here. Answers to 172.31.20.1 do not: that subnet
# is directly on the container's veth. Running this script again with the
# other mode switches it.
:if ($mode = "nat") do={
  :if ([:len [/ip/firewall/nat/find where comment="loft-lan-nat"]] = 0) do={
    /ip/firewall/nat/add chain=srcnat src-address-list=loft-lan out-interface=br-loft \
      action=masquerade comment=loft-lan-nat
  }
} else={
  :do { /ip/firewall/nat/remove [find where comment="loft-lan-nat"] } on-error={}
}

:put "hosts in address-list loft-lan now use table loft → 172.31.20.2"
:put ("LAN gateway mode: " . $mode . " — client.yaml listen.gateway must say the same")
:put "fronts stay on veth-loft inside the container (-if 172.31.20.1); they do not hairpin"
