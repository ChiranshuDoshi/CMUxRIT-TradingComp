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

def submit_order(ticker: str, qty: int, side: str, price: float = None, order_type="MARKET"):
    url = f"{API}/orders"
    order = {"ticker": ticker, "type": order_type, "quantity": qty, "action": side}
    if order_type == "LIMIT" and price is not None:
        order["price"] = price
    r = requests.post(url, headers=HDRS, json=order)
    if r.ok:
        print(f"Order placed: {side} {qty} {ticker} at {price if price else order_type}")
    else:
        print("Order failed:", r.text)

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
            for t in tenders:
                tender_id = t['tender_id']
                tender_price = float(t['price'])
                qty = int(t['quantity'])
                
                spread = tender_price - intrinsic_val
                print(f"Tender {tender_id}: Spread={spread:.2f} CAD")
                
                if spread >= Min_spread_threshold:
                    accept_tender(tender_id)
                    # Placeholder: unwind via market orders
                    submit_order("RITC","SELL", qty, order_type="MARKET")
                else:
                    reject_tender(tender_id)
            
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
