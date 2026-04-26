import time

# Assuming 'assets2' is your DataFrame and it has been created correctly.
for row in assets2.index:
    # Perform necessary logic on each row
    if assets2.loc[row, 'decision'] == 'SELL':
        ticker = assets2.loc[row, 'ticker']
        
        # Log the order for debugging
        print(f"Placing SELL order for {ticker}")
        
        # Example order format (replace with actual API call)
        order = {
            "ticker": ticker,
            "action": "SELL",
            "quantity": 100  # Adjust quantity as needed
        }
        
        # Place the order
        place_order(order)

    # Delay between requests to avoid API throttling
    time.sleep(0.5)

# Updated function to avoid invalid ticker error
def place_order(order):
    print(f"Placing order: {json.dumps(order, indent=4)}")  # Log order details for debugging
    order_url = "http://localhost:9999/v1/orders"
    response = session.post(order_url, json=order)
    
    if response.ok:
        print(f"Order placed: {order}")
    else:
        print(f"Failed to place order for {order['ticker']}: {response.status_code} - {response.text}")
