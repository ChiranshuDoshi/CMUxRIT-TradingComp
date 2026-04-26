# TradingAlgo[CMUxRIT]

Algorithmic trading strategies and experiments for the **RIT x CMU 2025** competition cases:
- **Algorithmic ETF Arbitrage**
- **Volatility Trading**

This repository includes multiple Python and Go implementations, including baseline scripts, evolved strategy variants, and duplicate snapshots used during iteration.

## What’s in this repo

### Main strategy groups
- **ETF Arbitrage (Python):** `arb.py`, `arb2.py`, `arb3.py`, `arb4.py`, `arb5.py`, `arb6.py`, `RITCxCMU 2025 Algorithmic ETF Arbitrage Case base script.py`
- **Volatility Trading (Python):** `RITCxCMU 2025 Volatility Trading Case base script.py` (+ `(1)` and `(2)` variants), `arb[1]/vol.py`
- **Go arbitrage module:** `arb[1]/` (module: `arbcase`)
- **Go volatility modules:** `RITCxCMU_VOLCASE/`, `vol[1]/RITCxCMU_VOLCASE/`, `vol2/RITCxCMU_VOLCASE/` (module: `volcase`)

### Other files
- `RITCxCMU 2025 Volatility Trading Case - Decision Support.xlsm` — decision support spreadsheet
- `arb[1]/log.csv` and `arb[1]/debug.py` — logging + quick profit aggregation utility
- `CMUQuantComp` and `CMUQuantComp.go` — placeholder/empty artifacts in current state

### Project docs and config
- `requirements.txt` — Python dependencies used across scripts
- `.env.example` — environment-variable template for API/config values
- `arb[1]/README.md` — Go ETF arbitrage module usage notes
- `RITCxCMU_VOLCASE/README.md` — Go volatility module usage notes

## Prerequisites

- **RIT Trading API simulator** running locally at `http://localhost:9999/v1`
- **Go** `1.22+`
- **Python** `3.8+` recommended
- Network access to local API and valid API key

> Most scripts hardcode `API` and `API_KEY`. Update these values inside each script before running.

## Python setup

Install common dependencies:

```powershell
python -m pip install --upgrade pip
python -m pip install requests numpy pandas py_vollib
```

Or install from the pinned repo file:

```powershell
python -m pip install -r requirements.txt
```

If `py_vollib` install fails on your Python version, try a compatible Python environment (often 3.8 works best) or install via conda.

## Running strategies

### 1) ETF Arbitrage (Python)

Baseline script:

```powershell
python "RITCxCMU 2025 Algorithmic ETF Arbitrage Case base script.py"
```

Variant scripts:

```powershell
python arb.py
python arb2.py
python arb3.py
python arb4.py
python arb5.py
python arb6.py
```

### 2) Volatility Trading (Python)

```powershell
python "RITCxCMU 2025 Volatility Trading Case base script.py"
```

Additional variants:

```powershell
python "RITCxCMU 2025 Volatility Trading Case base script (1).py"
python "RITCxCMU 2025 Volatility Trading Case base script (2).py"
python "arb[1]\vol.py"
```

### 3) ETF Arbitrage (Go)

```powershell
Set-Location "arb[1]"
go run .
```

### 4) Volatility Trading (Go)

Primary module:

```powershell
Set-Location "RITCxCMU_VOLCASE"
go run .
```

Snapshot variants:

```powershell
Set-Location "vol[1]\RITCxCMU_VOLCASE"
go run .

Set-Location "vol2\RITCxCMU_VOLCASE"
go run .
```

## Repository layout (high level)

```text
.
├─ arb.py, arb2.py, ... arb6.py                  # Python ETF arbitrage variants
├─ RITCxCMU 2025 Algorithmic ETF Arbitrage Case base script.py
├─ RITCxCMU 2025 Volatility Trading Case base script*.py
├─ arb[1]/                                       # Go ETF arbitrage module + Python helpers
│  ├─ api/
│  ├─ logic/
│  ├─ main.go
│  ├─ script.py
│  ├─ vol.py
│  └─ debug.py
├─ RITCxCMU_VOLCASE/                             # Go volatility module
│  ├─ api/
│  ├─ logic/
│  └─ main.go
├─ vol[1]/RITCxCMU_VOLCASE/                      # volatility snapshot
├─ vol2/RITCxCMU_VOLCASE/                        # volatility snapshot
├─ logic/                                        # shared/experimental Go logic
├─ main.go, main2.go, pairs.go, PairsG.go        # standalone Go strategy experiments
└─ go.mod                                        # root Go module (experimental state)
```

## Notes and caveats

- This repo contains **multiple parallel versions** of strategies; not all files are intended to be run together.
- Several files contain hardcoded API keys. For safety, move keys to environment variables before publishing broadly.
- Root-level Go files (`main.go`, `pairs.go`, `PairsG.go`, etc.) appear to be experimental standalone strategies.
- Use one module/folder at a time (`arb[1]`, `RITCxCMU_VOLCASE`, etc.) when running Go code.

## Suggested next cleanup steps

1. Centralize configuration (`API_BASE_URL`, `API_KEY`) via env vars.
2. Add per-module `README.md` files with strategy-specific behavior.
3. Keep `requirements.txt` updated and standardize one preferred script per case.
4. Remove or archive duplicate snapshots once final strategy is chosen.

---
Added in this repo:
- `requirements.txt`
- `.env.example`
- `arb[1]/README.md`
- `RITCxCMU_VOLCASE/README.md`
