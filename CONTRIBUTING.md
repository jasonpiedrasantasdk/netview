# Contributing

Thanks for your interest in contributing to `netview`.

## Development setup

- A Linux environment is recommended (capture uses `AF_PACKET` and process mapping uses `/proc`)
- Go installed (see `go.mod` for the minimum supported version)
- Root privileges may be required to run/test live capture features

## Getting started

```bash
git clone https://github.com/jasonpiedrasantasdk/netview.git
cd netview
go mod tidy
go build ./...
```

## Before submitting changes

- Format code: `gofmt -w .`
- Run basic checks: `go vet ./...` and `go build ./...`
- Keep changes focused and describe user-facing behavior/impact in the PR

## Reporting issues

Please include:

- OS/distribution, kernel, and architecture
- Go version (`go version`)
- Command/flags used and whether running with required privileges
- Expected vs actual behavior and relevant logs/output
- If related to saving captures, note whether a GUI picker (`zenity`/`kdialog`) was available

## Security & privacy

Do **not** attach pcap files, payload/hex dumps, or logs that contain sensitive traffic or local process information. Redact details before sharing.
