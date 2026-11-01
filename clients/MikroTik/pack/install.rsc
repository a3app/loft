# loft — once per router. Edit $disk, then paste into New Terminal.
# Uploads first:  <disk>/loft/loft.tar   (docker save)
#                 <disk>/loft/data/client.yaml
# Image arch must match /system/resource architecture-name (arm, arm64, x86).

:local disk "disk1"
:local vethIP "172.31.20.2/24"
:local rosIP "172.31.20.1/24"
:local vethGW "172.31.20.1"

:if ([:len [/file/find where name=($disk . "/loft/loft.tar")]] = 0) do={
  :error ("missing " . $disk . "/loft/loft.tar")
}
:if ([:len [/file/find where name=($disk . "/loft/data/client.yaml")]] = 0) do={
  :error ("missing " . $disk . "/loft/data/client.yaml — copy pack/client.yaml, set token and url")
}

:if ([:len [/interface/veth/find where name="veth-loft"]] = 0) do={
  /interface/veth/add name=veth-loft address=$vethIP gateway=$vethGW
}
:if ([:len [/interface/bridge/find where name="br-loft"]] = 0) do={
  /interface/bridge/add name=br-loft comment="loft container"
}
:if ([:len [/ip/address/find where interface="br-loft"]] = 0) do={
  /ip/address/add address=$rosIP interface=br-loft
}
:if ([:len [/interface/bridge/port/find where interface="veth-loft"]] = 0) do={
  /interface/bridge/port add bridge=br-loft interface=veth-loft
}
:if ([:len [/ip/firewall/nat/find where comment="loft-container"]] = 0) do={
  /ip/firewall/nat/add chain=srcnat action=masquerade src-address=172.31.20.0/24 comment=loft-container
}

/container/config/set tmpdir=($disk . "/loft/tmp")
:if ([:len [/container/mounts/find where list="loft-data"]] = 0) do={
  /container/mounts/add list=loft-data src=($disk . "/loft/data") dst=/data
}

:if ([:len [/container/find where name="loft"]] = 0) do={
  /container/add name=loft interface=veth-loft root-dir=($disk . "/loft/root") \
    mountlists=loft-data file=($disk . "/loft/loft.tar") \
    start-on-boot=yes logging=yes
} else={
  :put "container loft already exists — /container/print"
}

:put "wait until /container/print status=stopped, then /container/start loft"
:put "SOCKS5 172.31.20.2:1080  — log line: socks5 0.0.0.0:1080"
:put "GUI http://172.31.20.2:8090  — token, URL, uplink (ether1 / lte1 / …)"
:put "overlay up — log line: outbound ... up overlay="
:put "WAN pin — GUI Save then /import disk1/loft/data/uplink.rsc (or pack/uplink.rsc)"
:put "LAN via loft as gateway only after log line: tun up — then policy.rsc"
