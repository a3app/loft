@echo off
REM loft-routes 1.0
REM Keenetic eats this file as a normal .bat. Gateway is ignored.
REM Loft reads the "| via" / "| bypass" tag in the rem.
REM
route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube | via
route ADD 172.217.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube | via
route ADD 1.1.1.0 MASK 255.255.255.0 0.0.0.0 :: rem Cloudflare | via
route ADD 10.0.0.0 MASK 255.0.0.0 0.0.0.0 :: rem LAN | bypass
route ADD 192.168.0.0 MASK 255.255.0.0 0.0.0.0 :: rem LAN | bypass
