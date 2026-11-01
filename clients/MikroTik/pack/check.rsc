# loft — board check. Paste into New Terminal.
# Need RouterOS 7.12+, architecture arm / arm64 / x86 / x86_64, container package, disk.

:put ("loft check  host=" . [/system/identity/get name])
:put ("  version=" . [/system/resource/get version])
:put ("  architecture-name=" . [/system/resource/get architecture-name])
:put ("  cpu=" . [/system/resource/get cpu])
:put ("  total-memory=" . [/system/resource/get total-memory])

:local arch [/system/resource/get architecture-name]
:if ($arch = "arm" || $arch = "arm64" || $arch = "x86" || $arch = "x86_64") do={
  :put "  containers: this CPU can run them"
} else={
  :put "  containers: this CPU cannot (need arm, arm64, x86, or x86_64 / CHR)"
}

:if ([:len [/system/package/find where name="container"]] = 0) do={
  :put "  package container: missing — Extra packages for this version, then reboot"
} else={
  :local dis [/system/package/get [find where name="container"] disabled]
  :if ($dis) do={
    :put "  package container: installed, disabled — /system/package/enable container and apply-changes"
  } else={
    :put "  package container: enabled"
  }
}

:do {
  :local dm [/system/device-mode/get container]
  :if ($dm = true) do={
    :put "  device-mode container: yes"
  } else={
    :put "  device-mode container: no — /system/device-mode/update container=yes then tap Reset once (CHR: power cycle)"
  }
} on-error={
  :put "  device-mode: could not read — /system/device-mode/print"
}

:put "  disks:"
/disk/print
:put "next: copy pack/loft-linux-<arch>.tar and pack/client.yaml onto the disk, then install.rsc"
