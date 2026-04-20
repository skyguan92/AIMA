# U15 on `gb10-4T` ↔ `linux-1`

- Date: 2026-04-20
- Query host: `gb10-4T` (`qujing@100.91.39.109`, hostname `aitopatom-66c4`)
- Peer host: `linux-1` (`cjwx@100.121.255.97`, hostname `qujing24`)
- AIMA repo: `HEAD=44bc4c7e362d`

## Verdict

`PASS`

The fleet mDNS discovery and remote exec path worked between `gb10-4T` and the
existing `linux-1` service on the shared `192.168.108.0/22` LAN. The temporary
`gb10-4T` serve log did not emit Docker `veth` / `br-*` interface noise.

## What Was Verified

1. Network precondition was satisfied.
   - `gb10-4T` LAN IP: `192.168.108.131/22`
   - `linux-1` LAN IP: `192.168.109.23/22`
   - Both are on the same `192.168.108.0/22` broadcast domain.

2. A current `v0.4-dev` binary was started only on `gb10-4T` in an isolated
   directory and port.
   - Command: `~/aima-uat/u15/aima serve --addr 0.0.0.0:6295 --mdns --allow-insecure-no-auth`
   - `linux-1` reused its existing `aima-serve` at port `6188`, so no long-running
     inference workload was stopped or replaced.

3. `fleet devices` from `gb10-4T` discovered the `linux-1` peer via mDNS.
   - Discovered device: `qujing24`
   - Address: `192.168.109.23:6188`

4. `fleet info qujing24` returned real remote hardware details.
   - Hostname: `qujing24`
   - GPU: `NVIDIA GeForce RTX 4090`
   - GPU count: `2`

5. `fleet exec qujing24 hardware.detect` returned the remote HAL detect payload.
   - GPU: `NVIDIA GeForce RTX 4090`
   - GPU count: `2`
   - OS version: `22.04`

6. The temporary `gb10-4T` serve log showed normal startup only.
   - No `mdns: unexpected log line`
   - No Docker `veth` / `br-*` interface noise

## Evidence

- `00-serve.log`
- `01-fleet-devices.stderr`
- `01-fleet-devices.payload.json`
- `02-fleet-info-qujing24.stderr`
- `02-fleet-info-qujing24.payload.json`
- `03-fleet-exec-hardware-detect.stderr`
- `03-fleet-exec-hardware-detect.payload.json`
