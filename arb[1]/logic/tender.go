package logic

import (
	"arbcase/api"
	"context"
	"fmt"
	"log"
)

func TenderArb(ctx context.Context, baseURL, apiKey string) {
	tenders, err := api.GetTenders(ctx, baseURL, apiKey)
	if err != nil {
		log.Println("get tenders err:", err)
		return
	}
	if len(tenders) == 0 {
		return
	}

	// fetch current market data
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

	// get orderbooks
	obBull, _ := api.GetOrderBook(ctx, baseURL, apiKey, "BULL")
	obBear, _ := api.GetOrderBook(ctx, baseURL, apiKey, "BEAR")
	obRitc, _ := api.GetOrderBook(ctx, baseURL, apiKey, "RITC")

	const cushionPerShare = 0.02
	const threshold = 5000.0 // CAD profit needed
	const maxOrderSize = 10000

	for _, t := range tenders {
		qty := int(t.Quantity)

		// basket hedging prices
		buyBull, _ := avgFillPrice(obBull.Asks, qty)
		buyBear, _ := avgFillPrice(obBear.Asks, qty)
		sellBull, _ := avgFillPrice(obBull.Bids, qty)
		sellBear, _ := avgFillPrice(obBear.Bids, qty)

		// RITC market hedging prices
		buyRitc, _ := avgFillPrice(obRitc.Asks, qty)
		sellRitc, _ := avgFillPrice(obRitc.Bids, qty)

		var profitBasket, profitRitc float64
		var avgBasket, avgRitc float64

		if t.Action == "SELL" {
			totalSellRitcCAD := t.Price*fx*float64(qty) - ritc.TradingFee*float64(qty)
			totalBuyStocksCAD := (buyBull+bull.TradingFee)*float64(qty) +
				(buyBear+bear.TradingFee)*float64(qty)
			profitBasket = totalSellRitcCAD - totalBuyStocksCAD - cushionPerShare*float64(qty)

			profitRitc = (t.Price*fx-buyRitc*fx-ritc.TradingFee)*float64(qty) -
				cushionPerShare*float64(qty)
			avgBasket = buyBull + buyBear
			avgRitc = buyRitc*fx - ritc.TradingFee*fx
		} else {
			totalSellStocksCAD := (sellBull-bull.TradingFee)*float64(qty) +
				(sellBear-bear.TradingFee)*float64(qty)
			totalBuyRitcCAD := t.Price*fx*float64(qty) + ritc.TradingFee*float64(qty)
			profitBasket = totalSellStocksCAD - totalBuyRitcCAD - cushionPerShare*float64(qty)

			profitRitc = (sellRitc*fx-ritc.TradingFee*fx)*float64(qty) -
				t.Price*fx*float64(qty) - cushionPerShare*float64(qty)

			avgBasket = sellBull + sellBear
			avgRitc = sellRitc*fx - ritc.TradingFee*fx
		}

		bestProfit := profitBasket
		bestMethod := "basket"
		if profitRitc > bestProfit {
			bestProfit = profitRitc
			bestMethod = "ritc"
		}

		fmt.Printf("Tender %d %s qty=%d price=%.2f profitBasket=%.2f avgBasket=%.2f profitRitc=%.2f avgRitc=%.2f best=%s %.2f\n",
			t.TenderID, t.Action, qty, t.Price, profitBasket, avgBasket, profitRitc, avgRitc, bestMethod, bestProfit)

		if bestProfit > threshold && bestMethod == "ritc" && (maxQuantity-(2*abs(quantity)) > int(t.Quantity)) {
			if err := api.AcceptTender(ctx, baseURL, apiKey, t.TenderID); err != nil {
				log.Println("accept tender err:", err)
				continue
			}

			// break hedge into chunks
			for remaining := qty; remaining > 0; {
				chunk := remaining
				if chunk > maxOrderSize {
					chunk = maxOrderSize
				}

				if t.Action == "SELL" {
					// we sold RITC to tender
					if bestMethod == "basket" {
						_ = api.PlaceOrder(ctx, baseURL, apiKey,
							api.Order{Ticker: "BULL", Quantity: chunk, Action: "BUY", Type: "MARKET"})
						_ = api.PlaceOrder(ctx, baseURL, apiKey,
							api.Order{Ticker: "BEAR", Quantity: chunk, Action: "BUY", Type: "MARKET"})
					} else {
						_ = api.PlaceOrder(ctx, baseURL, apiKey,
							api.Order{Ticker: "RITC", Quantity: chunk, Action: "BUY", Type: "MARKET"})
					}
				} else {
					// we bought RITC from tender
					if bestMethod == "basket" {
						_ = api.PlaceOrder(ctx, baseURL, apiKey,
							api.Order{Ticker: "BULL", Quantity: chunk, Action: "SELL", Type: "MARKET"})
						_ = api.PlaceOrder(ctx, baseURL, apiKey,
							api.Order{Ticker: "BEAR", Quantity: chunk, Action: "SELL", Type: "MARKET"})
					} else {
						_ = api.PlaceOrder(ctx, baseURL, apiKey,
							api.Order{Ticker: "RITC", Quantity: chunk, Action: "SELL", Type: "MARKET"})
					}
				}

				remaining -= chunk
			}
		}
	}
}
