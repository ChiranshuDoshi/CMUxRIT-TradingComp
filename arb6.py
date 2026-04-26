import requests
import time
import sys

# ===============================================
# RIT API Configuration
# ===============================================
API = "http://localhost:9999/v1"
API_KEY = "18WWG30P"
HDRS = {"X-API-key": API_KEY}

# ===============================================
# Parameters
# ===============================================
Transaction_fee = 0.02  # per share
Min_spread_threshold = 0.05  # CAD
ETF_multiplier = 2
Max_order_size = 10000

# ===============================================
# Portfolio tracking
# ===============================================
portfolio = {
    'BULL': 0,
    'BEAR': 0,
    'RITC': 0
}

# ---------------- GLOBAL STATE ----------------
active_tender = None

s = requests.Session()
s.headers.update(HDRS)

# ===============================================
# Helper Functions
# ===============================================
def get_last_price(ticker: str) -> float:
    url = f"{API}/securities/book?ticker={ticker}"
    r = requests.get(url, headers=HDRS)
    if r.ok:
        data = r.json()
        if data["bids"] and data["asks"]:
            best_bid = float(data["bids"][0]["price"])
            best_ask = float(data["asks"][0]["price"])
            return (best_bid + best_ask) / 2
    return None

def submit_order(ticker, action, qty, order_type="MARKET", price=None):
    params = {
        "ticker": ticker,
        "type": order_type.upper(),
        "quantity": int(qty),
        "action": action.upper()
    }
    if order_type.upper() == "LIMIT":
        if price is None:
            raise ValueError("Price must be provided for LIMIT orders")
        params["price"] = price
    resp = s.post(f"{API}/orders", params=params)
    try:
        resp.raise_for_status()
        return resp.json()
    except Exception as e:
        print("Order submission failed:", e, resp.text)
        return None

def get_tenders():
    url = f"{API}/tenders"
    r = requests.get(url, headers=HDRS)
    if r.ok:
        return r.json()
    return []

def accept_tender(tender_id: int):
    url = f"{API}/tenders/{tender_id}"
    # No JSON body needed if API accepts it in URL
    r = requests.post(url, headers=HDRS)
    if r.ok:
        print(f"Accepted tender {tender_id}")
    else:
        print("Tender accept failed:", r.text)


def reject_tender(tender_id: int):
    url = f"{API}/tenders/{tender_id}"
    r = requests.post(url, headers=HDRS, json={"action": "REJECT"})
    if r.ok:
        print(f"Rejected tender {tender_id}")
    else:
        print("Tender reject failed:", r.text)

def convert_etf_to_cad(ETF_price_usd, usd_to_cad):
    return ETF_price_usd * usd_to_cad

def best_bid_ask(ticker):
    r = s.get(f"{API}/securities/book", params={"ticker": ticker})
    r.raise_for_status()
    book = r.json()
    bid = float(book["bids"][0]["price"]) if book["bids"] else 0.0
    ask = float(book["asks"][0]["price"]) if book["asks"] else 1e12
    return bid, ask

# ===============================================
# Tender Profit & Stop-Loss
# ===============================================
def book_tender_profit():
    global active_tender
    if not active_tender or "side" not in active_tender or "price" not in active_tender:
        return

    bid, ask = best_bid_ask("RITC")
    side = active_tender["side"]
    tender_price = active_tender["price"]
    lot = max(1, active_tender["quantity"] // 10)

    try:
        if side == "BUY" and bid >= tender_price + 0.3:
            qty = min(lot, active_tender["remaining"])
            submit_order("RITC", "SELL", qty)
            active_tender["remaining"] -= qty
            print(f"Booked profit: SOLD {qty} @ {bid}")

        elif side == "SELL" and ask <= tender_price - 0.3:
            qty = min(lot, active_tender["remaining"])
            submit_order("RITC", "BUY", qty)
            active_tender["remaining"] -= qty
            print(f"Booked profit: BOUGHT {qty} @ {ask}")

    except Exception as e:
        print("Order failed:", e)
        return

    if active_tender["remaining"] <= 0:
        print("Tender fully squared off.")
        active_tender = None

def apply_tender_stoploss():
    global active_tender
    if not active_tender or "side" not in active_tender:
        return

    bid, ask = best_bid_ask("RITC")
    side = active_tender["side"]
    tender_price = active_tender["price"]
    lot = max(1, active_tender["quantity"] // 10)
    stop_loss_threshold = 0.3

    try:
        if side == "BUY" and bid <= tender_price - stop_loss_threshold:
            qty = min(lot, active_tender["remaining"])
            submit_order("RITC", "SELL", qty)
            active_tender["remaining"] -= qty
            print(f"STOP LOSS: SOLD {qty} @ {bid}")

        elif side == "SELL" and ask >= tender_price + stop_loss_threshold:
            qty = min(lot, active_tender["remaining"])
            submit_order("RITC", "BUY", qty)
            active_tender["remaining"] -= qty
            print(f"STOP LOSS: BOUGHT {qty} @ {ask}")

    except Exception as e:
        print("Stop-loss order failed:", e)

    if active_tender["remaining"] <= 0:
        print("Stop-loss fully exited tender.")
        active_tender = None

# ===============================================
# Tender Handling & Arbitrage
# ===============================================
def evaluate_tender_opportunity(new_tender):
    global active_tender
    if not new_tender:
        return

    tender_id = new_tender['tender_id']
    new_side = new_tender.get('action')  # BUY or SELL
    new_price = float(new_tender.get('price'))

    if active_tender:
        old_side = active_tender["side"]
        old_price = active_tender["price"]

        # BUY→SELL arbitrage
        if old_side == "BUY" and new_side == "SELL" and new_price > old_price:
            profit_per_share = new_price - old_price
            print(f"Arbitrage: Closing long via SELL tender, locking profit {profit_per_share:.2f}")
            accept_tender(tender_id)
            active_tender = None
            return

        # SELL→BUY arbitrage
        elif old_side == "SELL" and new_side == "BUY" and new_price < old_price:
            profit_per_share = old_price - new_price
            print(f"Arbitrage: Closing short via BUY tender, locking profit {profit_per_share:.2f}")
            accept_tender(tender_id)
            active_tender = None
            return

    # No arbitrage, handled normally in handle_tenders
    return

def handle_tenders(tenders):
    global active_tender
    if not tenders:
        return

    for t in tenders:
        evaluate_tender_opportunity(t)
        tender_id = t['tender_id']
        t_side = t.get('action')
        tender_price = float(t.get('price'))
        t_qty = abs(int(t.get('quantity')))

        # Skip if already handled via arbitrage
        if active_tender and active_tender.get("side") == t_side and active_tender.get("price") == tender_price:
            continue

        if active_tender is None:
            accept_tender(tender_id)
            active_tender = {
                "side": t_side,
                "price": tender_price,
                "quantity": t_qty,
                "remaining": t_qty
            }
            print("Accepted tender:", active_tender)
            break

# ===============================================
# Core Algorithm Loop
# ===============================================
def algorithm_loop():
    while True:
        try:
            bull = get_last_price("BULL")
            bear = get_last_price("BEAR")
            ritc = get_last_price("RITC")
            usd = get_last_price("USD")

            if None in [bull, bear, ritc, usd]:
                print("Error fetching prices, retrying...")
                time.sleep(1)
                continue

            intrinsic_val = bull + bear
            ritc_cad = convert_etf_to_cad(ritc, usd)

            tenders = get_tenders()
            handle_tenders(tenders)

            # Book profits / stop-loss
            if active_tender:
                book_tender_profit()
                apply_tender_stoploss()

            time.sleep(1)

        except KeyboardInterrupt:
            print("Stopping algorithm...")
            sys.exit()
        except Exception as e:
            print("Error in algorithm loop:", e)
            time.sleep(1)

# ===============================================
# Main Function
# ===============================================
def main():
    print("Starting Algorithmic ETF Arbitrage Bot...")
    algorithm_loop()

if __name__ == "__main__":
    main()
