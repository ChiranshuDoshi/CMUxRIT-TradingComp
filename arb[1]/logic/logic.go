package logic

import (
	"arbcase/api"
	"context"
	"fmt"
	"log"
	"sync"
)

var (
	posMu     sync.Mutex
	positions = map[string]int{
		"RITC": 0,
		"BULL": 0,
		"BEAR": 0,
	}
)

func addPosition(ticker string, delta int) {
	posMu.Lock()
	positions[ticker] += delta
	posMu.Unlock()
}

func getPosition(ticker string) int {
	posMu.Lock()
	v := positions[ticker]
	posMu.Unlock()
	return v
}

func CheckArbitrageAndTrade(ctx context.Context, baseURL, apiKey string) {
	// 1. Get securities (includes live positions)
	secs, err := api.GetSecurities(ctx, baseURL, apiKey)
	if err != nil {
		log.Println("securities err:", err)
		return
	}

	// extract needed securities
	var usd, bull, bear, ritc api.Security
	for _, s := range secs {
		switch s.Ticker {
		case "USD":
			usd = s
		case "BULL":
			bull = s
		case "BEAR":
			bear = s
		case "RITC":
			ritc = s
		}
	}

	fx := (usd.Bid + usd.Ask) / 2
	if fx <= 0 || fx < 0.9 || fx > 1.1 {
		log.Println("invalid fx:", fx)
		return
	}

	// live positions directly from the API
	bullPos := int(bull.Position)
	bearPos := int(bear.Position)
	ritcPos := int(ritc.Position)

	feeBull := (bull.TradingFee)
	feeBear := (bear.TradingFee)
	feeRitc := (ritc.TradingFee)

	// 2. Pull order books concurrently
	var (
		obBull, obBear, obRitc    api.OrderBook
		errBull, errBear, errRitc error
	)
	var wgBooks sync.WaitGroup
	wgBooks.Add(3)
	go func() {
		defer wgBooks.Done()
		obBull, errBull = api.GetOrderBook(ctx, baseURL, apiKey, "BULL")
	}()
	go func() {
		defer wgBooks.Done()
		obBear, errBear = api.GetOrderBook(ctx, baseURL, apiKey, "BEAR")
	}()
	go func() {
		defer wgBooks.Done()
		obRitc, errRitc = api.GetOrderBook(ctx, baseURL, apiKey, "RITC")
	}()
	wgBooks.Wait()
	if errBull != nil || errBear != nil || errRitc != nil {
		log.Println("book errs:", errBull, errBear, errRitc)
		return
	}

	const maxQty = 10000  // API max order size per leg
	const threshold = 100 // CAD profit needed
	const cushionPerShare = 0.02

	// 3. Compute market prices and available quantities
	buyBull, fillBuyBull := avgFillPrice(obBull.Asks, maxQty)
	sellBull, fillSellBull := avgFillPrice(obBull.Bids, maxQty)

	buyBear, fillBuyBear := avgFillPrice(obBear.Asks, maxQty)
	sellBear, fillSellBear := avgFillPrice(obBear.Bids, maxQty)

	buyRitcUSD, fillBuyRitc := avgFillPrice(obRitc.Asks, maxQty)
	sellRitcUSD, fillSellRitc := avgFillPrice(obRitc.Bids, maxQty)

	sizeSellRitc := min3(fillSellRitc, fillBuyBull, fillBuyBear)
	sizeBuyRitc := min3(fillBuyRitc, fillSellBull, fillSellBear)

	// 4. Compute total CAD profit including fees
	var profitA, profitB float64
	if sizeSellRitc > 0 {
		totalSellRitcCAD := (sellRitcUSD*fx - feeRitc*fx) * float64(sizeSellRitc)
		totalBuyStocksCAD := ((buyBull + feeBull) + (buyBear + feeBear)) * float64(sizeSellRitc)
		profitA = totalSellRitcCAD - totalBuyStocksCAD - cushionPerShare*float64(sizeSellRitc)
	}
	if sizeBuyRitc > 0 {
		totalSellStocksCAD := ((sellBull - feeBull) + (sellBear - feeBear)) * float64(sizeBuyRitc)
		totalBuyRitcCAD := (buyRitcUSD*fx + feeRitc*fx) * float64(sizeBuyRitc)
		profitB = totalSellStocksCAD - totalBuyRitcCAD - cushionPerShare*float64(sizeBuyRitc)
	}

	fmt.Printf("ProfitCAD SELL_RITC/BUY_STOCKS=%.2f (size=%d) | ProfitCAD BUY_RITC/SELL_STOCKS=%.2f (size=%d)\n",
		profitA, sizeSellRitc, profitB, sizeBuyRitc)

	// helper to send three legs concurrently
	SendTriad := func(orders []api.Order) error {
		var wg sync.WaitGroup
		errs := make(chan error, len(orders))
		wg.Add(len(orders))
		for _, o := range orders {
			go func(ord api.Order) {
				defer wg.Done()
				err := api.PlaceOrder(ctx, baseURL, apiKey, ord)
				errs <- err
			}(o)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				return e
			}
		}
		return nil
	}

	// 5. Open new arb (each leg clamped to ≤ maxQty)
	if profitA > threshold && sizeSellRitc > 0 {
		qty := sizeSellRitc
		if qty > maxQty {
			qty = maxQty
		}
		fmt.Printf("[OPEN] SELL RITC/BUY STOCKS qty=%d\n", qty)
		_ = SendTriad([]api.Order{
			{Ticker: "RITC", Quantity: qty, Action: "SELL", Type: "MARKET"},
			{Ticker: "BULL", Quantity: qty, Action: "BUY", Type: "MARKET"},
			{Ticker: "BEAR", Quantity: qty, Action: "BUY", Type: "MARKET"},
		})
	}
	if profitB > threshold && sizeBuyRitc > 0 {
		qty := sizeBuyRitc
		if qty > maxQty {
			qty = maxQty
		}
		fmt.Printf("[OPEN] BUY RITC/SELL STOCKS qty=%d\n", qty)
		_ = SendTriad([]api.Order{
			{Ticker: "RITC", Quantity: qty, Action: "BUY", Type: "MARKET"},
			{Ticker: "BULL", Quantity: qty, Action: "SELL", Type: "MARKET"},
			{Ticker: "BEAR", Quantity: qty, Action: "SELL", Type: "MARKET"},
		})
	}

	// 6. Close positions chunked ≤ maxQty using live positions and breaking out on error
	if ritcPos > 0 && bullPos < 0 && bearPos < 0 && profitA > threshold {
		remaining := min3(abs(ritcPos), abs(bullPos), abs(bearPos))
		for remaining > 0 {
			qty := remaining
			if qty > maxQty {
				qty = maxQty
			}
			// also cap by visible liquidity if needed:
			avail := min4(qty, int(ritc.BidSize), int(bull.AskSize), int(bear.AskSize))
			if avail <= 0 {
				log.Println("no liquidity available to close, stopping")
				break
			}
			qty = avail

			fmt.Printf("[CLOSE] chunk SELL stocks/BUY RITC qty=%d\n", qty)
			err := SendTriad([]api.Order{
				{Ticker: "RITC", Quantity: qty, Action: "SELL", Type: "MARKET"},
				{Ticker: "BULL", Quantity: qty, Action: "BUY", Type: "MARKET"},
				{Ticker: "BEAR", Quantity: qty, Action: "BUY", Type: "MARKET"},
			})
			if err != nil {
				log.Printf("close chunk failed: %v, aborting loop", err)
				break
			}

			// refresh live positions from API before next iteration
			secs, _ = api.GetSecurities(ctx, baseURL, apiKey)
			for _, s := range secs {
				switch s.Ticker {
				case "BULL":
					bullPos = int(s.Position)
				case "BEAR":
					bearPos = int(s.Position)
				case "RITC":
					ritcPos = int(s.Position)
				}
			}
			remaining = min3(abs(ritcPos), abs(bullPos), abs(bearPos))
		}
	}

	if ritcPos < 0 && bullPos > 0 && bearPos > 0 && profitB > threshold {
		remaining := min3(abs(ritcPos), abs(bullPos), abs(bearPos))
		for remaining > 0 {
			qty := remaining
			if qty > maxQty {
				qty = maxQty
			}
			avail := min4(qty, int(ritc.AskSize), int(bull.BidSize), int(bear.BidSize))
			if avail <= 0 {
				log.Println("no liquidity available to close, stopping")
				break
			}
			qty = avail

			fmt.Printf("[CLOSE] chunk BUY stocks/SELL RITC qty=%d\n", qty)
			err := SendTriad([]api.Order{
				{Ticker: "RITC", Quantity: qty, Action: "BUY", Type: "MARKET"},
				{Ticker: "BULL", Quantity: qty, Action: "SELL", Type: "MARKET"},
				{Ticker: "BEAR", Quantity: qty, Action: "SELL", Type: "MARKET"},
			})
			if err != nil {
				log.Printf("close chunk failed: %v, aborting loop", err)
				break
			}

			secs, _ = api.GetSecurities(ctx, baseURL, apiKey)
			for _, s := range secs {
				switch s.Ticker {
				case "BULL":
					bullPos = int(s.Position)
				case "BEAR":
					bearPos = int(s.Position)
				case "RITC":
					ritcPos = int(s.Position)
				}
			}
			remaining = min3(abs(ritcPos), abs(bullPos), abs(bearPos))
		}
	}
}

//not taking positions even if opprtunity on opposite side, change quantity and treshold

func avgFillPrice(levels []api.BookLevel, qty int) (float64, int) {
	remaining := qty
	total := 0.0
	filled := 0
	for _, lv := range levels {
		if remaining <= 0 {
			break
		}
		take := int(lv.Quantity)
		if take > remaining {
			take = remaining
		}
		total += float64(take) * lv.Price
		remaining -= take
		filled += take
	}
	if filled == 0 {
		return 0, 0
	}
	return total / float64(filled), filled
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func min3(a, b, c int) int    { return min(min(a, b), c) }
func min4(a, b, c, d int) int { return min(min(min(a, b), c), d) }

func avgFillPriceExt(levels []api.BookLevel, qty int) (avgPrice float64, filled int, worstPrice float64) {
	remaining := qty
	totalCost := 0.0
	filled = 0
	worstPrice = 0

	for _, lv := range levels {
		if remaining <= 0 {
			break
		}
		take := int(lv.Quantity)
		if take > remaining {
			take = remaining
		}
		totalCost += float64(take) * lv.Price
		remaining -= take
		filled += take
		worstPrice = lv.Price // last level we touched
	}

	if filled == 0 {
		return 0, 0, 0
	}
	avgPrice = totalCost / float64(filled)
	return avgPrice, filled, worstPrice
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
