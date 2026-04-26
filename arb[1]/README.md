# `arb[1]` — ETF Arbitrage (Go module)

Go implementation of the RIT x CMU ETF arbitrage strategy with tender logic.

## Module

- `go.mod` module name: `arbcase`
- Entry point: `main.go`
- Key packages:
  - `api/` — REST wrappers for case, securities, orders, tenders
  - `logic/` — arbitrage and tender decision logic

## Run

From this folder:

```powershell
go run .
```

Or from repo root:

```powershell
go run .\arb[1]
```

## Configuration

Current `main.go` uses hardcoded values:
- base URL: `http://localhost:9999/v1`
- API key: set in `main.go`

Recommended: wire these to env vars (`RIT_API_BASE_URL`, `RIT_API_KEY`) for safer sharing.

## Extra utilities

- `script.py` — Python ETF arbitrage baseline/variant in same folder
- `vol.py` — Python volatility script variant
- `log.csv` + `debug.py` — quick PnL log inspection helper
