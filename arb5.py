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
s.headers.update(HDRS)  # {"X-API-key": "Rotman"}


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
    """
    Submit an order to the Rotman API.
    
    ticker: str -> e.g. "RITC", "BULL"
    action: str -> "BUY" or "SELL"
    qty: int   -> number of shares
    order_type: str -> "MARKET" (default) or "LIMIT"
    price: float -> required if LIMIT order
    """
    params = {
        "ticker": ticker,
        "type": order_type.upper(),
        "quantity": int(qty),
        "action": action.upper()
    }
    
    # For LIMIT orders, include the price
    if order_type.upper() == "LIMIT":
        if price is None:
            raise ValueError("Price must be provided for LIMIT orders")
        params["price"] = price

    resp = s.post(f"{API}/orders", params=params)
    try:
        resp.raise_for_status()
        return resp.json()   # full order response (order_id, status, etc.)
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
    r = requests.post(url, headers=HDRS, json={"action": "ACCEPT"})
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
    # Returns best bid and ask prices for a ticker
    r = s.get(f"{API}/securities/book", params={"ticker": ticker})
    r.raise_for_status()
    book = r.json()
    bid = float(book["bids"][0]["price"]) if book["bids"] else 0.0
    ask = float(book["asks"][0]["price"]) if book["asks"] else 1e12
    return bid, ask


def book_tender_profit():
    global active_tender
    if not active_tender or "side" not in active_tender or "price" not in active_tender:
        return

    bid, ask = best_bid_ask("RITC")
    side = active_tender["side"]
    tender_price = active_tender["price"]
    lot = max(1, active_tender["quantity"] // 10)

    try:
        if side == "BUY" and bid >= tender_price + 0.5:
            qty = min(lot, active_tender["remaining"])
            submit_order("RITC", "SELL", qty)
            active_tender["remaining"] -= qty
            print(f"Booked profit: SOLD {qty} @ {bid}")

        elif side == "SELL" and ask <= tender_price - 0.5:
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
    if not active_tender:
        return

    bid, ask = best_bid_ask("RITC")
    side, tender_price = active_tender["side"], active_tender["price"]
    lot = max(1, active_tender["quantity"] // 10)

    stop_loss_threshold = 0.5  # you can tune this

    if side == "BUY" and bid <= tender_price - stop_loss_threshold:
        # Long position losing → sell to stop further loss
        qty = min(lot, active_tender["remaining"])
        submit_order("RITC", "SELL", qty)
        active_tender["remaining"] -= qty
        print(f"STOP LOSS: SOLD {qty} @ {bid}")

    elif side == "SELL" and ask >= tender_price + stop_loss_threshold:
        # Short position losing → buy to stop further loss
        qty = min(lot, active_tender["remaining"])
        submit_order("RITC", "BUY", qty)
        active_tender["remaining"] -= qty
        print(f"STOP LOSS: BOUGHT {qty} @ {ask}")

    if active_tender["remaining"] <= 0:
        print("Stop-loss fully exited tender.")
        active_tender = None



def handle_tenders(tenders):
    """
    Accept new tenders if no active tender.
    Updates global active_tender.
    """
    global active_tender
    if not tenders:
        return

    for t in tenders:
        # First check for instant arbitrage opportunity
        evaluate_tender_opportunity(t)

        tender_id = t['tender_id']
        tender_price = float(t['price'])
        t_side = t.get('action')  # "BUY" or "SELL"
        t_qty = abs(int(t['quantity']))

        # If this tender was already handled by evaluate_tender_opportunity, skip it
        if (
            active_tender is not None
            and active_tender["side"] == t_side
            and active_tender["price"] == tender_price
        ):
            continue

        # Accept only if no active tender
        if active_tender is None:
            accept_tender(tender_id)
            active_tender = {
                "side": t_side,
                "price": tender_price,
                "quantity": t_qty,
                "remaining": t_qty
            }
            print("Accepted tender:", active_tender)
            # Only accept the first tender per tick
            break


def evaluate_tender_opportunity(new_tender):
    global active_tender
    
    
    if not active_tender:
        # no active tender, just accept this one
        accept_tender(new_tender)
        return

    # Check for arbitrage
    old_side, old_price = active_tender["side"], active_tender["price"]
    new_side, new_price = new_tender["side"], new_tender["price"]

    # Case 1: Already long (BUY tender), new tender is SELL
    if old_side == "BUY" and new_side == "SELL" and new_price > old_price:
        profit_per_share = new_price - old_price
        print(f"Arbitrage: Closing long via SELL tender, locking profit {profit_per_share:.2f}")
        accept_tender(new_tender)  # closes old position
        active_tender = None  # reset

    # Case 2: Already short (SELL tender), new tender is BUY
    elif old_side == "SELL" and new_side == "BUY" and new_price < old_price:
        profit_per_share = old_price - new_price
        print(f"Arbitrage: Closing short via BUY tender, locking profit {profit_per_share:.2f}")
        accept_tender(new_tender)  # closes old position
        active_tender = None

    else:
        # If no arbitrage opportunity, maybe ignore or evaluate as usual
        print("No arbitrage, handling as normal tender...")







# ===============================================
# Core Algorithm Loop
# ===============================================
def algorithm_loop():
    while True:
        try:
            # Fetch live market prices
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
            
            # Evaluate tenders
            tenders = get_tenders()
            handle_tenders(tenders)

# Book profits if price moved favorably
            book_tender_profit()
            apply_tender_stoploss()
                
            
            time.sleep(1)  # adjust for frequency of API calls
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

# ===============================================
# Entry Point
# ===============================================
if __name__ == "__main__":
    main()
