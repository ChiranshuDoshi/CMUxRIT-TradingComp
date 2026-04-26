"""
RIT ETF Arbitrage Executor
 - Actively takes arbitrage trades and evaluates/accepts tenders.
 - Designed to run with the RIT local REST API at API = "http://localhost:9999/v1"
"""

import requests, time, sys
from typing import Dict, Tuple, List

API = "http://localhost:9999/v1"
API_KEY = "18WWG30P"
HDRS = {"X-API-key": API_KEY}

# Instruments
BULL = "BULL"
BEAR = "BEAR"
RITC = "RITC"
USD  = "USD"
CAD  = "CAD"

# Market params
FEE_MKT = 0.02
REBATE_LMT = 0.01
CONVERTER_COST = 1500.0
CONVERTER_LOT = 10000

# Execution / risk
MAX_SIZE_EQUITY = 10000
ORDER_CHUNK = 2500        # child order size per leg
MAX_GROSS = 500000
MAX_LONG_NET = 25000
MAX_SHORT_NET = -25000

# Decision thresholds (CAD per ETF-unit)
ARB_THRESHOLD = 0.07      # minimum edge to attempt arb
URGENT_MULT = 2.0         # edge multiplier to force market execution
LIMIT_WAIT_SECS = 0.6     # how long to wait for passive limit to fill before fallback
SLEEP_BETWEEN = 0.12

# Session
s = requests.Session()
s.headers.update(HDRS)

# ---------- Helper wrappers ----------
def safe_get(path: str, params: dict = None, timeout: float = 2.0):
    try:
        r = s.get(f"{API}{path}", params=params, timeout=timeout)
        r.raise_for_status()
        return r.json()
    except Exception as e:
        print("[GET ERR]", path, e, file=sys.stderr)
        return None

def required_arb_threshold(snapshot, buffer=0.01):
    """
    Calculates the minimum CAD profit per ETF unit required to attempt arbitrage.
    Includes spread cost, fees, and a safety buffer.
    """
    spread_cost = ((snapshot["bull_ask"] - snapshot["bull_bid"]) +
                   (snapshot["bear_ask"] - snapshot["bear_bid"])) / 2
    base_fees = 0.04   # 2 trades * 0.02 per leg
    return spread_cost + base_fees + buffer

def get_current_RITC_position():
    """
    Returns the current net position of RITC in shares.
    Positive = long, Negative = short
    """
    # Example using a global positions dictionary
    # positions = {"RITC": 35000, "BULL": 5000, "BEAR": -2000}
    return positions.get("RITC", 0)


import numpy as np

def is_trending(snapshot_history, action, last_n=10):
    # use only the last N ticks
    recent = snapshot_history[-last_n:]
    
    # compute basket midpoint trend
    mids = [(s['bull_bid'] + s['bull_ask'] + s['bear_bid'] + s['bear_ask']) / 2 for s in recent]
    
    if action == "BUY":
        return all(earlier <= later for earlier, later in zip(mids, mids[1:]))  # trending up
    else:  # SELL
        return all(earlier >= later for earlier, later in zip(mids, mids[1:]))  # trending down



def close_profitable_positions(snapshot, profit_threshold=0.15):
    """
    Closes open positions when arbitrage edge exceeds profit_threshold (CAD per ETF unit).
    Checks both directions.
    """
    pos = positions_map()
    # Convert RITC to CAD
    usd_mid = (snapshot["usd_bid"] + snapshot["usd_ask"]) / 2
    ritc_cad = (snapshot["ritc_bid_usd"] + snapshot["ritc_ask_usd"]) / 2 * usd_mid
    basket_sell = snapshot["bull_bid"] + snapshot["bear_bid"]
    basket_buy  = snapshot["bull_ask"] + snapshot["bear_ask"]

    # Check if basket rich vs ETF
    if pos[BULL] < 0 and pos[BEAR] < 0 and pos[RITC] > 0:
        edge1 = basket_sell - ritc_cad
        if edge1 >= profit_threshold:
            print(f"[CLOSE] PROFIT-TAKE: edge1={edge1:.4f}")
            units = min(abs(pos[BULL]), abs(pos[BEAR]), pos[RITC])
            exec_buy_basket_units(units)
            place_market(RITC, "SELL", units)

    # Check if ETF rich vs basket
    if pos[BULL] > 0 and pos[BEAR] > 0 and pos[RITC] < 0:
        edge2 = ritc_cad - basket_buy
        if edge2 >= profit_threshold:
            print(f"[CLOSE] PROFIT-TAKE: edge2={edge2:.4f}")
            units = min(pos[BULL], pos[BEAR], abs(pos[RITC]))
            exec_sell_basket_units(units)
            place_market(RITC, "BUY", units)





def safe_post(path: str, params: dict = None, timeout: float = 3.0):
    try:
        r = s.post(f"{API}{path}", params=params, timeout=timeout)
        r.raise_for_status()
        return r
    except Exception as e:
        print("[POST ERR]", path, e, file=sys.stderr)
        return None
    
def calc_slippage(snapshot):
    """
    Estimates slippage based on bid/ask spread of BULL and BEAR.
    """
    return max(0.01, (snapshot["bull_ask"] - snapshot["bull_bid"]) / 2 +
                     (snapshot["bear_ask"] - snapshot["bear_bid"]) / 2)


# ---------- Market data ----------
def best_bid_ask(ticker: str) -> Tuple[float, float]:
    book = safe_get("/securities/book", {"ticker": ticker})
    if not book:
        return 0.0, 1e12
    bids = book.get("bids", [])
    asks = book.get("asks", [])
    bid = float(bids[0]["price"]) if bids else 0.0
    ask = float(asks[0]["price"]) if asks else 1e12
    return bid, ask

def positions_map() -> Dict[str,int]:
    data = safe_get("/securities")
    out = {}
    if data:
        for d in data:
            out[d.get("ticker")] = int(d.get("position", 0))
    for k in (BULL, BEAR, RITC, USD, CAD):
        out.setdefault(k, 0)
    return out

# ---------- Orders ----------
def place_market(ticker: str, action: str, qty: int) -> bool:
    params = {"ticker": ticker, "type": "MARKET", "quantity": int(qty), "action": action}
    r = safe_post("/orders", params=params)
    return bool(r and r.ok)

def place_limit(ticker: str, action: str, qty: int, price: float) -> bool:
    params = {"ticker": ticker, "type": "LIMIT", "quantity": int(qty), "action": action, "price": float(price)}
    r = safe_post("/orders", params=params)
    return bool(r and r.ok)

# ---------- Limits ----------
def within_limits_after(delta: Dict[str,int]) -> bool:
    pos = positions_map()
    for t,d in delta.items():
        pos[t] = pos.get(t,0) + d
    gross = abs(pos[BULL]) + abs(pos[BEAR]) + 2 * abs(pos[RITC])
    net   = pos[BULL] + pos[BEAR] + 2 * pos[RITC]
    return (gross <= MAX_GROSS) and (MAX_SHORT_NET <= net <= MAX_LONG_NET)

# ---------- Tender logic ----------
def get_active_tenders() -> List[dict]:
    j = safe_get("/tenders")
    return j if j else []

def mid(bid: float, ask: float) -> float:
    if ask > 1e11:
        return bid
    return (bid + ask) / 2.0



def evaluate_tender_offer(offer: dict, snapshot: dict) -> Tuple[bool, float, dict]:
    """
    Evaluates tender profitability conservatively.
    Accept only if expected profit clears ARB_THRESHOLD after slippage/fees.
    Returns (accept_bool, est_profit_CAD_per_ETF, detail_dict)
    """
    price_usd = float(offer.get("price", 0.0))
    size = int(offer.get("size", 1))
    usd_mid = (snapshot["usd_bid"] + snapshot["usd_ask"]) / 2
    tender_cad = price_usd * usd_mid

    basket_buy_cost = snapshot["bull_ask"] + snapshot["bear_ask"]
    slippage = calc_slippage(snapshot)
    fee_two_legs = 2 * FEE_MKT
    converter_per_share = CONVERTER_COST / CONVERTER_LOT

    # Conservative estimates
    est_market_profit = tender_cad - basket_buy_cost - fee_two_legs - slippage
    est_converter_profit = tender_cad - basket_buy_cost - converter_per_share

    # Decide which unwind path
    chosen = "converter" if (converter_per_share < (fee_two_legs + slippage) and size >= CONVERTER_LOT) else "market"
    est_used = max(est_market_profit, est_converter_profit)

    # Use stricter acceptance: must clear ARB_THRESHOLD dynamically
    dynamic_threshold = required_arb_threshold(snapshot)
    accept = est_used >= dynamic_threshold

    details = {"size": size,
               "tender_cad": tender_cad,
               "basket_buy_cost": basket_buy_cost,
               "est_market_profit": est_market_profit,
               "est_converter_profit": est_converter_profit,
               "chosen": chosen,
               "est_used": est_used,
               "dynamic_threshold": dynamic_threshold}
    return accept, est_used, details


# ---------- Execution patterns ----------
def exec_buy_basket_units(units: int) -> bool:
    # buys units of basket (1 BULL + 1 BEAR each)
    remain = units
    while remain > 0:
        q = min(ORDER_CHUNK, remain)
        delta = {BULL: q, BEAR: q}
        if not within_limits_after(delta):
            print("[LIMITS] cannot buy more basket due to limits")
            return False
        ok1 = place_market(BULL, "BUY", q)
        ok2 = place_market(BEAR, "BUY", q)
        if not (ok1 and ok2):
            print("[EXEC ERR] market basket buy child failed")
            return False
        remain -= q
        time.sleep(SLEEP_BETWEEN)
    return True

def basket_capacity_allows(action: str, tender_size: int, security="RITC", portfolio=None, 
                           gross_limit=25000, net_limit=25000, multiplier=2) -> bool:
    """
    Check if we can accept this tender without exceeding limits.
    action: "BUY" or "SELL"
    tender_size: number of shares
    security: "RITC", "BULL", "BEAR"
    portfolio: current positions dict
    multiplier: ETF multiplier (2 for RITC)
    """
    if portfolio is None:
        portfolio = {'BULL': 0, 'BEAR': 0, 'RITC': 0}

    # Determine proposed position change
    if action == "BUY":
        delta = tender_size
    elif action == "SELL":
        delta = -tender_size
    else:
        raise ValueError("Invalid action: must be 'BUY' or 'SELL'")

    # Apply multiplier for ETF
    if security == "RITC":
        delta_effective = delta * multiplier
    else:
        delta_effective = delta

    # Calculate projected gross and net positions
    projected_gross = sum(abs(v) for v in portfolio.values()) + abs(delta_effective)
    projected_net = sum(portfolio.values()) + delta_effective

    if projected_gross <= gross_limit and abs(projected_net) <= net_limit:
        return True
    else:
        return False


def exec_sell_basket_units(units: int) -> bool:
    remain = units
    while remain > 0:
        q = min(ORDER_CHUNK, remain)
        delta = {BULL: -q, BEAR: -q}
        if not within_limits_after(delta):
            print("[LIMITS] cannot sell more basket due to limits")
            return False
        ok1 = place_market(BULL, "SELL", q)
        ok2 = place_market(BEAR, "SELL", q)
        if not (ok1 and ok2):
            print("[EXEC ERR] market basket sell child failed")
            return False
        remain -= q
        time.sleep(SLEEP_BETWEEN)
    return True

def redeem_converter(units: int) -> bool:
    # attempt redemption via converter endpoint; fallback to market if remainder exists
    uses = units // CONVERTER_LOT
    remainder = units % CONVERTER_LOT
    for i in range(uses):
        params = {"action": "REDEEM", "ticker": "RITC", "quantity": CONVERTER_LOT}
        r = safe_post("/converters", params=params)
        if not (r and r.ok):
            print("[CONV ERR] converter redeem failed")
            return False
        time.sleep(SLEEP_BETWEEN)
    if remainder > 0:
        return exec_buy_basket_units(remainder)
    return True

# ---------- Arbitrage decision & execution ----------
def try_arb_edge(edge: float, direction: int, snapshot: dict) -> bool:
    """
    direction: 1 => basket rich (sell basket, buy ETF)
               2 => ETF rich (buy basket, sell ETF)
    edge: CAD per ETF unit
    """
    # Dynamic threshold
    min_edge = required_arb_threshold(snapshot)
    if edge < min_edge:
        print(f"[ARB] skip: edge {edge:.4f} < dynamic threshold {min_edge:.4f}")
        return False

    # Scale trade size by profit cushion
    trade_units = int(min(MAX_SIZE_EQUITY, max(ORDER_CHUNK, (edge - min_edge) * 10000)))
    trade_units = max(ORDER_CHUNK, trade_units)

    if direction == 1:  # basket rich
        delta = {BULL: -trade_units, BEAR: -trade_units, RITC: trade_units}
        if not within_limits_after(delta):
            print("[LIMITS] skip arb1: limits exceeded")
            return False
        exec_sell_basket_units(trade_units)
        place_market(RITC, "BUY", trade_units)
        return True
    else:  # direction 2, ETF rich
        delta = {BULL: trade_units, BEAR: trade_units, RITC: -trade_units}
        if not within_limits_after(delta):
            print("[LIMITS] skip arb2: limits exceeded")
            return False
        exec_buy_basket_units(trade_units)
        place_market(RITC, "SELL", trade_units)
        return True
    
import time

def accept_tender_with_trailing_stop(offer: dict, snapshot: dict):
    """
    Executes tender based on market comparison with trailing stop logic.
    """
    tender_id = offer.get("tender_id")
    tender_price_usd = float(offer.get("price", 0.0))
    tender_size = int(offer.get("size", 1))
    is_fixed = offer.get("is_fixed_bid", False)
    
    usd_mid = (snapshot["usd_bid"] + snapshot["usd_ask"]) / 2
    tender_cad = tender_price_usd * usd_mid

    # Determine market reference
    basket_mid = snapshot["bull_ask"] + snapshot["bear_ask"]

    # Determine action: BUY tender (if tender < market), SELL tender (if tender > market)
    if tender_cad < basket_mid - 0.1:
        action = "BUY"
        print(f"[TENDER] BUY tender id={tender_id} tender_cad={tender_cad:.4f} basket_mid={basket_mid:.4f}")
    elif tender_cad > basket_mid + 0.1:
        action = "SELL"
        print(f"[TENDER] SELL tender id={tender_id} tender_cad={tender_cad:.4f} basket_mid={basket_mid:.4f}")
    else:
        print(f"[TENDER] SKIP tender id={tender_id}, not profitable")
        return False

    # Accept tender
    params = {} if is_fixed else {"price": tender_price_usd}
    r = safe_post(f"/tenders/{tender_id}", params=params)
    if not (r and r.ok):
        print(f"[TENDER] accept failed id={tender_id}")
        return False

    # Start unwind loop with trailing stop logic
    start_time = time.time()
    max_profit_cad = tender_cad + 1.0   # maximum take profit
    min_exit_cad = tender_cad - 0.15    # trailing stop if losing
    exit_done = False

    while not exit_done and time.time() - start_time < 30:
        # update market snapshot
        bull_bid, bull_ask = best_bid_ask(BULL)
        bear_bid, bear_ask = best_bid_ask(BEAR)
        usd_bid, usd_ask = best_bid_ask(USD)
        usd_mid = (usd_bid + usd_ask) / 2
        basket_mid_now = bull_ask + bear_ask
        ritc_bid, ritc_ask = best_bid_ask(RITC)
        ritc_cad = ritc_bid * usd_mid if action == "SELL" else ritc_ask * usd_mid

        # Profit calculation
        if action == "BUY":
            profit = basket_mid_now - tender_cad
        else:  # SELL tender
            profit = tender_cad - basket_mid_now

        # Check take profit or trailing stop
        if profit >= max_profit_cad:
            print(f"[TENDER] TAKE PROFIT id={tender_id} profit={profit:.4f}")
            exit_done = True
        elif profit <= min_exit_cad:
            print(f"[TENDER] TRAILING STOP id={tender_id} profit={profit:.4f}")
            exit_done = True

        time.sleep(0.5)

    # Execute unwind market orders
    units = tender_size
    if action == "BUY":
        exec_buy_basket_units(units)
        place_market(RITC, "SELL", units)
    else:
        exec_sell_basket_units(units)
        place_market(RITC, "BUY", units)

    print(f"[TENDER] UNWIND done id={tender_id}")
    return True



# ---------- Tender acceptance + unwind ----------
import time

import time
import numpy as np

def accept_tender_predicted(offer: dict, snapshot: dict, snapshot_history: list, open_tenders: list, RITC_max=125_000):
    tender_id = offer.get("tender_id")
    tender_price_usd = float(offer.get("price", 0.0))
    tender_size = int(offer.get("size", 1))
    is_fixed = offer.get("is_fixed_bid", False)

    usd_mid = (snapshot["usd_bid"] + snapshot["usd_ask"]) / 2
    tender_cad = tender_price_usd * usd_mid

    # Basket midpoint
    basket_mid = (snapshot["bull_bid"] + snapshot["bull_ask"] + snapshot["bear_bid"] + snapshot["bear_ask"]) / 2

    # Decide initial action
    if tender_cad < basket_mid - 0.1:
        action = "BUY"
    elif tender_cad > basket_mid + 0.1:
        action = "SELL"
    else:
        print(f"[TENDER] SKIP tender id={tender_id}")
        return False

    # Trend filter
    if not is_trending(snapshot_history, action, last_n=5):
        print(f"[TENDER] SKIP tender id={tender_id} due to adverse trend")
        return False

    # Profit enhancement: check if new tender can hedge/monetize old tender
    # But don’t block if no old tender profit
    profit_possible = False
    for old_tender in open_tenders:
        old_action = old_tender["action"]
        old_price_cad = old_tender["price_cad"]
        if action == "BUY" and old_action == "SELL" and tender_cad < old_price_cad:
            profit_possible = True
        elif action == "SELL" and old_action == "BUY" and tender_cad > old_price_cad:
            profit_possible = True

    if profit_possible:
        print(f"[TENDER] New tender {tender_id} can net profit vs old tender")

    # Accept tender regardless, profit_possible only informs strategy
    params = {} if is_fixed else {"price": tender_price_usd}
    r = safe_post(f"/tenders/{tender_id}", params=params)
    if not (r and r.ok):
        print(f"[TENDER] accept failed id={tender_id}")
        return False

    # Gradual offload logic as before, dynamically sized by limits
    qty_take_profit = int(tender_size * 0.8)
    qty_hold = tender_size - qty_take_profit
    sold_qty = 0
    EPS = 1e-4
    start_time = time.time()

    max_basket_seen = float('-inf')
    min_basket_seen = float('inf')

    while time.time() - start_time < 10:
        bull_bid, bull_ask = best_bid_ask(BULL)
        bear_bid, bear_ask = best_bid_ask(BEAR)
        basket_now = (bull_bid + bull_ask + bear_bid + bear_ask) / 2

        if action == "BUY":
            max_basket_seen = max(max_basket_seen, basket_now)
            take_profit_condition = max_basket_seen >= tender_cad + 0.05 - EPS
            trailing_stop_condition = basket_now <= tender_cad - 0.15
            current_position = get_current_RITC_position()
            max_chunk = min(10_000, max(0, current_position))
        else:
            min_basket_seen = min(min_basket_seen, basket_now)
            take_profit_condition = min_basket_seen <= tender_cad - 0.05 + EPS
            trailing_stop_condition = basket_now >= tender_cad + 0.15
            current_position = get_current_RITC_position()
            max_chunk = min(10_000, max(0, RITC_max - current_position))

        # Gradual offload
        if take_profit_condition and max_chunk > 0 and sold_qty < qty_take_profit:
            exec_qty = min(max_chunk, qty_take_profit - sold_qty)
            if action == "BUY":
                exec_buy_basket_units(exec_qty)
                place_market(RITC, "SELL", exec_qty)
            else:
                exec_sell_basket_units(exec_qty)
                place_market(RITC, "BUY", exec_qty)
            sold_qty += exec_qty
            print(f"[TENDER] chunk TAKE PROFIT id={tender_id}, sold_qty={sold_qty}")

        # Trailing stop
        if trailing_stop_condition:
            remaining_qty = qty_take_profit - sold_qty
            if remaining_qty > 0:
                if action == "BUY":
                    exec_buy_basket_units(remaining_qty)
                    place_market(RITC, "SELL", remaining_qty)
                else:
                    exec_sell_basket_units(remaining_qty)
                    place_market(RITC, "BUY", remaining_qty)
                print(f"[TENDER] TRAILING STOP id={tender_id}, remaining_qty={remaining_qty}")
            break

        if sold_qty >= qty_take_profit:
            break

        time.sleep(0.1)

    print(f"[TENDER] 20% qty remains for id={tender_id}, size={qty_hold}")
    return True



open_tenders = []





# ---------- Main loop ----------
def main_loop():
    print("[START] arb executor running")
    snapshot_history = []

    while True:
        case = safe_get("/case")
        if not case:
            time.sleep(0.5)
            continue
        if case.get("status") != "ACTIVE":
            print("[INFO] case not active; sleeping")
            time.sleep(1.0)
            continue

        # market snapshot
        bull_bid, bull_ask = best_bid_ask(BULL)
        bear_bid, bear_ask = best_bid_ask(BEAR)
        ritc_bid_usd, ritc_ask_usd = best_bid_ask(RITC)
        usd_bid, usd_ask = best_bid_ask(USD)
        usd_mid = mid(usd_bid, usd_ask)
        ritc_bid_cad = ritc_bid_usd * usd_mid
        ritc_ask_cad = ritc_ask_usd * usd_mid
        basket_sell = bull_bid + bear_bid
        basket_buy  = bull_ask + bear_ask
        edge1 = basket_sell - ritc_ask_cad
        edge2 = ritc_bid_cad - basket_buy

        snapshot = {
            "bull_bid": bull_bid, "bull_ask": bull_ask,
            "bear_bid": bear_bid, "bear_ask": bear_ask,
            "ritc_bid_usd": ritc_bid_usd, "ritc_ask_usd": ritc_ask_usd,
            "usd_bid": usd_bid, "usd_ask": usd_ask,
            "ritc_bid_cad": ritc_bid_cad, "ritc_ask_cad": ritc_ask_cad
        }

        # Maintain short-term snapshot history
        snapshot_history.append(snapshot)
        if len(snapshot_history) > 10:
            snapshot_history.pop(0)

        # 1️⃣ Close profitable positions first
        close_profitable_positions(snapshot, profit_threshold=0.15)

        # 2️⃣ Tendors (predictive)
        tenders = get_active_tenders()
        for t in tenders:
            accept_tender_predicted(t, snapshot, snapshot_history, open_tenders)


        # 3️⃣ Then arbitrage legs
        if edge1 >= ARB_THRESHOLD:
            print(f"[CHECK] edge1={edge1:.4f} >= {ARB_THRESHOLD:.4f}")
            try_arb_edge(edge1, 1, snapshot)
        elif edge2 >= ARB_THRESHOLD:
            print(f"[CHECK] edge2={edge2:.4f} >= {ARB_THRESHOLD:.4f}")
            try_arb_edge(edge2, 2, snapshot)

        time.sleep(0.5)


if __name__ == "__main__":
    try:
        main_loop()
    except KeyboardInterrupt:
        print("\n[STOP] terminated by user")
