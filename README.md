# netview

`netview` is a Linux terminal UI for live network connection monitoring. It tracks flows, identifies protocols and endpoints, maps traffic to local processes, lets you inspect packet history and payload bytes, and can save captured packets to a pcap file.

> Companion project: [netportid](https://github.com/jasonpiedrasantasdk/netportid) — a port-centric view focused on local ports and owning processes.

## Features

- Live packet capture on selected interfaces or all active interfaces
- Connection/flow tracking with inbound, outbound, or combined views
- Protocol detection heuristics for TCP/UDP traffic, including TLS (SNI, version, ALPN), HTTP, DNS, QUIC, SSH, and DHCP
- Process attribution via `/proc` socket-to-PID mapping
- Search/filter connections; toggle LAN and DNS visibility; cycle interfaces
- Detail panel with **Summary**, **Packets**, and **Hex** views (metadata, recent packet summaries, payload hex/ASCII)
- Save captured packets to `.pcap` (GUI file picker via `zenity`/`kdialog` when available, with a fallback path)

## Screenshot

<!-- TODO: add screenshot/demo -->
_Screenshot coming soon._

## Requirements

- **Linux** (uses `AF_PACKET` capture and `/proc` for process mapping)
- **Root privileges** (or equivalent raw-capture capabilities) to capture packets
- **Go 1.22+** to build from source (the module currently declares `go 1.22.0`)
- **Optional (save dialog):** `zenity` (GNOME) or `kdialog` (KDE). If unavailable, captures are saved to the default path.

## Install / Build

### Build from source

```bash
git clone https://github.com/jasonpiedrasantasdk/netview.git
cd netview
go build -o netview .
```

Run:

```bash
sudo ./netview [flags]
```

### Install with Go

Once a tagged release is available:

```bash
go install github.com/jasonpiedrasantasdk/netview@latest
sudo "$(go env GOPATH)/bin/netview"
```

## Usage

```bash
sudo netview [-i iface1,iface2]
```

**Flags**

- `-i` — Comma-separated interfaces to monitor (default: all active, non-loopback interfaces)

## Controls

- `q` / `Ctrl+C` — Quit
- `s` — Save capture (`y`: save & quit, `n`: save & keep capturing, `Esc`: cancel)
- `w` — Switch view: inbound / outbound / both
- `p` — Pause/resume capture updates
- `l` — Toggle LAN traffic visibility (hidden by default)
- `d` — Toggle DNS traffic visibility (hidden by default)
- `i` — Cycle interface filter
- `↑`/`↓` or `j`/`k` — Navigate connections
- `Enter` — Toggle detail panel
- `+` — Expand/collapse selected row
- `Tab` / `←` / `→` — Switch detail tabs (Summary, Packets, Hex)
- `g` / `G` — Jump to top/bottom
- `r` — Refresh process map
- `/` — Search/filter (`Esc` cancel, `Enter` apply, `Backspace` edit)
- `?` — Toggle help

## Saving captures

- Default filename format: `netview-YYYY-MM-DDTHH-MM-SS.pcap`
- If a GUI picker is available (`zenity`/`kdialog`), you can choose the destination.
- Fallback/default directory: `~/netViewSaves/` (created if needed)

## How it works & limitations

- Connections are assembled from observed packets and enriched with heuristic protocol detection and `/proc`-based process mapping.
- Packet/event handling, history, and payload previews are bounded; under high load some events may be dropped.
- Protocol identification and payload visibility depend on traffic type and what can be observed on the wire.
- Linux-only.

## Security & privacy

`netview` can display and export sensitive information, including endpoint addresses, SNI/DNS/HTTP metadata, local process details, and raw packet/payload bytes in hex views and pcap files. Handle captures carefully, store them securely, and use this tool only on systems and networks you are authorized to monitor.

## Author

- [Jason Piedrasanta](https://software.piedrasanta.space)

## License

[MIT](LICENSE) © 2026 Jason Piedrasanta
