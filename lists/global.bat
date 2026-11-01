@echo off
REM loft-routes 1.0
REM zone: global
REM
route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube
route ADD 172.217.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube
route ADD 74.125.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube
route ADD 142.251.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube
route ADD 173.194.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube
route ADD 216.58.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube
route ADD 1.1.1.0 MASK 255.255.255.0 0.0.0.0 :: rem Cloudflare
route ADD 104.16.0.0 MASK 255.248.0.0 0.0.0.0 :: rem Cloudflare
route ADD 45.57.0.0 MASK 255.255.0.0 0.0.0.0 :: rem Netflix
route ADD 108.175.32.0 MASK 255.255.240.0 0.0.0.0 :: rem Netflix
