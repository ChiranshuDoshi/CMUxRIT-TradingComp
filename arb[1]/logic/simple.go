package logic

import (
	"arbcase/api"
	"context"
	"fmt"
	"log"
	"sync"
)

var quantity int = 0

const maxQuantity = 75000

// func Arb(ctx context.Context, baseURL, apiKey string) {

// 	secs, err := api.GetSecurities(ctx, baseURL, apiKey)
// 	if err != nil {
// 		log.Println("securities err:", err)
// 		return
// 	}

// 	var usd, bull, bear, ritc api.Security
// 	for _, s := range secs {
// 		switch s.Ticker {
// 		case "USD":
// 			usd = s
// 		case "BULL":
// 			bull = s
// 		case "BEAR":
// 			bear = s
// 		case "RITC":
// 			ritc = s
// 		}
// 	}

// 	fx := (usd.Bid + usd.Ask) / 2
// 	if fx <= 0 || fx < 0.9 || fx > 1.1 {
// 		log.Println("invalid fx:", fx)
// 		return
// 	}

// 	const maxQty = 1000  // API max order size per leg
// 	const threshold = 60 // CAD profit needed
// 	const cushionPerShare = 0.02

// 	profitA := (((bull.Bid - bull.TradingFee) + (bear.Bid - bear.TradingFee)) -
// 		(ritc.Ask*usd.Ask + ritc.TradingFee) -
// 		cushionPerShare) * float64(maxQty)
// 	profitB := (-((bull.Ask - bull.TradingFee) + (bear.Ask - bear.TradingFee)) +
// 		(ritc.Bid*usd.Bid + ritc.TradingFee) -
// 		cushionPerShare) * float64(maxQty)

// 	fmt.Printf("ProfitCAD SELL_RITC/BUY_STOCKS=%.2f  | ProfitCAD BUY_RITC/SELL_STOCKS=%.2f \n",
// 		profitB, profitA)

// 	SendTriad := func(orders []api.Order) error {
// 		var wg sync.WaitGroup
// 		errs := make(chan error, len(orders))
// 		wg.Add(len(orders))
// 		for _, o := range orders {
// 			go func(ord api.Order) {
// 				defer wg.Done()
// 				err := api.PlaceOrder(ctx, baseURL, apiKey, ord)
// 				errs <- err
// 			}(o)
// 		}
// 		wg.Wait()
// 		close(errs)
// 		for e := range errs {
// 			if e != nil {
// 				return e
// 			}
// 		}
// 		return nil
// 	}

// 	if profitA > threshold {
// 		qty := maxQty
// 		fmt.Printf("[OPEN] BUY RITC/SELL STOCKS qty=%d (profitA=%.2f)\n", qty, profitA)
// 		fmt.Printf(
// 			"[OPEN] BUY RITC/SELL STOCKS qty=%d | RITC ask USD %.2f CAD %.2f | BULL bid %.2f | BEAR bid %.2f\n",
// 			qty,
// 			ritc.Ask,
// 			ritc.Ask*fx,
// 			bull.Bid,
// 			bear.Bid,
// 		)
// 		quantity += qty
// 		_ = SendTriad([]api.Order{
// 			{Ticker: "RITC", Quantity: qty, Action: "BUY", Type: "MARKET"},
// 			{Ticker: "BULL", Quantity: qty, Action: "SELL", Type: "MARKET"},
// 			{Ticker: "BEAR", Quantity: qty, Action: "SELL", Type: "MARKET"},
// 		})
// 	}

// 	if profitB > threshold {
// 		qty := maxQty
// 		fmt.Printf("[OPEN] SELL RITC/BUY STOCKS qty=%d (profitB=%.2f)\n", qty, profitB)
// 		fmt.Printf(
// 			"[OPEN] SELL RITC/BUY STOCKS qty=%d | RITC bid USD %.2f CAD %.2f | BULL ask %.2f | BEAR ask %.2f\n",
// 			qty,
// 			ritc.Bid,
// 			ritc.Bid*fx,
// 			bull.Ask,
// 			bear.Ask,
// 		)
// 		quantity -= qty
// 		_ = SendTriad([]api.Order{
// 			{Ticker: "RITC", Quantity: qty, Action: "SELL", Type: "MARKET"},
// 			{Ticker: "BULL", Quantity: qty, Action: "BUY", Type: "MARKET"},
// 			{Ticker: "BEAR", Quantity: qty, Action: "BUY", Type: "MARKET"},
// 		})
// 	}

// 	log.Println(quantity)

// }

func Arb(ctx context.Context, baseURL, apiKey string, tick int) {
	secs, err := api.GetSecurities(ctx, baseURL, apiKey)
	if err != nil {
		log.Println("securities err:", err)
		return
	}

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

	const maxQty = 1000   // API max order size per leg
	const threshold = 150 // CAD profit needed
	const cushionPerShare = 0.02

	// get order books
	obBull, errBull := api.GetOrderBook(ctx, baseURL, apiKey, "BULL")
	obBear, errBear := api.GetOrderBook(ctx, baseURL, apiKey, "BEAR")
	obRitc, errRitc := api.GetOrderBook(ctx, baseURL, apiKey, "RITC")
	if errBull != nil || errBear != nil || errRitc != nil {
		log.Println("orderbook err:", errBull, errBear, errRitc)
		return
	}

	// compute average fill for each side
	buyBull, fillBuyBull := avgFillPrice(obBull.Asks, maxQty)
	sellBull, fillSellBull := avgFillPrice(obBull.Bids, maxQty)

	buyBear, fillBuyBear := avgFillPrice(obBear.Asks, maxQty)
	sellBear, fillSellBear := avgFillPrice(obBear.Bids, maxQty)

	buyRitc, fillBuyRitc := avgFillPrice(obRitc.Asks, maxQty)
	sellRitc, fillSellRitc := avgFillPrice(obRitc.Bids, maxQty)

	// only trade if we can fill the full maxQty on each leg
	if fillBuyBull < maxQty || fillBuyBear < maxQty || fillSellBull < maxQty || fillSellBear < maxQty ||
		fillBuyRitc < maxQty || fillSellRitc < maxQty {
		log.Println("not enough depth to fill", maxQty)
		return
	}

	// Profit of BUY RITC / SELL STOCKS
	profitBuyRitcSellStocks := (((sellBull - bull.TradingFee) + (sellBear - bear.TradingFee)) -
		(buyRitc*usd.Ask + ritc.TradingFee) -
		cushionPerShare) * float64(maxQty)

	// Profit of SELL RITC / BUY STOCKS
	profitSellRitcBuyStocks := ((buyRitc*usd.Bid + ritc.TradingFee) -
		((buyBull + bull.TradingFee) + (buyBear + bear.TradingFee)) -
		cushionPerShare) * float64(maxQty)

	fmt.Printf("ProfitCAD SELL_RITC/BUY_STOCKS=%.2f  | ProfitCAD BUY_RITC/SELL_STOCKS=%.2f\n",
		profitSellRitcBuyStocks, profitBuyRitcSellStocks)

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

	// BUY RITC / SELL STOCKS
	if profitBuyRitcSellStocks > threshold && (tick < 280 || quantity < 0) {
		qty := maxQty
		quantity += qty
		fmt.Printf("[OPEN] BUY RITC/SELL STOCKS qty=%d | RITC ask USD %.2f CAD %.2f | BULL bid %.2f | BEAR bid %.2f\n",
			qty,
			buyRitc,
			buyRitc*fx,
			sellBull,
			sellBear,
		)
		_ = SendTriad([]api.Order{
			{Ticker: "RITC", Quantity: qty, Action: "BUY", Type: "MARKET"},
			{Ticker: "BULL", Quantity: qty, Action: "SELL", Type: "MARKET"},
			{Ticker: "BEAR", Quantity: qty, Action: "SELL", Type: "MARKET"},
		})
	}

	// SELL RITC / BUY STOCKS
	if profitSellRitcBuyStocks > threshold && (tick < 280 || quantity > 0) {
		qty := maxQty
		quantity -= qty
		fmt.Printf("[OPEN] SELL RITC/BUY STOCKS qty=%d | RITC bid USD %.2f CAD %.2f | BULL ask %.2f | BEAR ask %.2f\n",
			qty,
			sellRitc,
			sellRitc*fx,
			buyBull,
			buyBear,
		)
		_ = SendTriad([]api.Order{
			{Ticker: "RITC", Quantity: qty, Action: "SELL", Type: "MARKET"},
			{Ticker: "BULL", Quantity: qty, Action: "BUY", Type: "MARKET"},
			{Ticker: "BEAR", Quantity: qty, Action: "BUY", Type: "MARKET"},
		})
	}
}
