# `RITCxCMU_VOLCASE` — Volatility Trading (Go module)

Primary Go implementation of the volatility trading case.

## Module

- `go.mod` module name: `volcase`
- Entry point: `main.go`
- Key packages:
  - `api/` — REST clients (`case`, `news`, `securities`, `orders`, `tenders`)
  - `logic/` — trading logic, helper functions, and risk/position structures

## Run

From this folder:

```powershell
go run .
```

Or from repo root:

```powershell
go run .\RITCxCMU_VOLCASE
```

## Behavior

- Polls case tick from local RIT API
- Calls `logic.VolTrader(...)` in a continuous loop
- Uses hardcoded API key and base URL in `main.go`

## Related snapshots

- `vol[1]/RITCxCMU_VOLCASE/`
- `vol2/RITCxCMU_VOLCASE/`

These appear to be strategy snapshots/iterations of this same module.
