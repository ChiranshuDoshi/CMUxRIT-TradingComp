package logic

import (
	"context"
	"log"
	"math"
	"strconv"
	"volcase/api"
)

var openPositions []OptionPosition
var rtmPosition int

func updateIV(iv float64) {
	for i := range openPositions {
		openPositions[i].TargetIV = iv
	}
}

func VolTrader(ctx context.Context, baseURL, apiKey string, tick int) {
	// 1. Get latest news and parse forecast
	news, err := api.GetNews(ctx, baseURL, apiKey)
	if err != nil || len(news) == 0 {
		log.Println("news err:", err)
		return
	}
	latest := news[0]
	forecast := ParseVolForecast(latest.Body, tick)
	if forecast.Type == "" {
		log.Println("no forecast found in news:", latest.Body)
		return
	}

	// skip if forecast week is already past
	currentWeek := (tick / 75) + 1
	if forecast.AppliesWeek != 0 && forecast.AppliesWeek < currentWeek {
		log.Printf("forecast applies to week %d but current week %d, skipping\n",
			forecast.AppliesWeek, currentWeek)
		return
	}

	log.Println(forecast.Type)
	log.Println(forecast.High, forecast.Low)

	// 2. Get underlying RTM price
	secs, err := api.GetSecurities(ctx, baseURL, apiKey)
	if err != nil {
		log.Println("GetSecurities err:", err)
		return
	}
	var etf api.Security
	for _, s := range secs {
		if s.Ticker == "RTM" {
			etf = s
			break
		}
	}
	S := (etf.Bid + etf.Ask) / 2

	// 3. Snap to nearest allowed strike
	allowed := []int{48, 49, 50, 51, 52}
	bestK, bestDiff := allowed[0], math.MaxFloat64
	for _, k := range allowed {
		if d := math.Abs(float64(k) - S); d < bestDiff {
			bestDiff, bestK = d, k
		}
	}
	strike := bestK
	callTicker := "RTM" + strconv.Itoa(strike) + "C"
	putTicker := "RTM" + strconv.Itoa(strike) + "P"
	log.Println(callTicker, putTicker)

	// 4. Get order books
	obCall, errCall := api.GetOrderBook(ctx, baseURL, apiKey, callTicker)
	obPut, errPut := api.GetOrderBook(ctx, baseURL, apiKey, putTicker)
	if errCall != nil || errPut != nil ||
		len(obCall.Asks) == 0 || len(obCall.Bids) == 0 ||
		len(obPut.Asks) == 0 || len(obPut.Bids) == 0 {
		log.Println("orderbook err:", errCall, errPut)
		return
	}
	midCall := (obCall.Asks[0].Price + obCall.Bids[0].Price) / 2
	midPut := (obPut.Asks[0].Price + obPut.Bids[0].Price) / 2

	// 5. Compute implied vol
	ivCall := CalcImpliedVol(midCall, S, float64(strike), true, tick)
	ivPut := CalcImpliedVol(midPut, S, float64(strike), false, tick)
	iv := (ivCall + ivPut) / 2
	log.Println("current iv:", iv)

	// 5.5 Close positions if target reached
	CheckAndClosePositions(ctx, baseURL, apiKey, iv)

	// // *** New: safer roll logic ***
	// if tick < 225 { // don’t roll at the very end
	// 	weekTick := tick % 75
	// 	// only roll if >60 ticks left in week (weekTick < 15)
	// 	if weekTick < 15 {
	// 		for _, pos := range openPositions {
	// 			posStrike := strikeFromTicker(pos.CallTicker)
	// 			if math.Abs(float64(posStrike-strike)) >= 3 {
	// 				RollPosition(ctx, baseURL, apiKey, pos, strike, iv)
	// 			}
	// 		}
	// 	}
	// }

	// thresholds adjusted by week & tick
	incdect, rngt, fixt := getThresholds(currentWeek, tick)

	// 6. Decide action & size dynamically
	var orders []api.Order
	T := timeToExpiry(tick)

	switch forecast.Type {
	case "increase":
		gap := forecast.High - iv
		if gap > incdect {
			qty := chooseQty(gap-incdect, T, 14, 50)
			if qty > 0 && checkRiskLimits("BUY", qty) {
				orders = []api.Order{
					{Ticker: callTicker, Quantity: qty, Action: "BUY", Type: "MARKET"},
					{Ticker: putTicker, Quantity: qty, Action: "BUY", Type: "MARKET"},
				}
			}
		}
	case "decrease":
		gap := iv - forecast.Low
		if gap > incdect {
			qty := chooseQty(gap-incdect, T, 14, 50)
			if qty > 0 && checkRiskLimits("SELL", qty) {
				orders = []api.Order{
					{Ticker: callTicker, Quantity: qty, Action: "SELL", Type: "MARKET"},
					{Ticker: putTicker, Quantity: qty, Action: "SELL", Type: "MARKET"},
				}
			}
		}
	case "range":
		if iv+rngt < forecast.Low {
			qty := chooseQty(forecast.Low-iv, T, 14, 50)
			if qty > 0 && checkRiskLimits("BUY", qty) {
				orders = []api.Order{
					{Ticker: callTicker, Quantity: qty, Action: "BUY", Type: "MARKET"},
					{Ticker: putTicker, Quantity: qty, Action: "BUY", Type: "MARKET"},
				}
			}
		} else if iv-rngt > forecast.High {
			qty := chooseQty(iv-forecast.High, T, 14, 50)
			if qty > 0 && checkRiskLimits("SELL", qty) {
				orders = []api.Order{
					{Ticker: callTicker, Quantity: qty, Action: "SELL", Type: "MARKET"},
					{Ticker: putTicker, Quantity: qty, Action: "SELL", Type: "MARKET"},
				}
			}
		}
	case "fixed":
		if iv+fixt < forecast.Low {
			qty := chooseQty(forecast.Low-iv, T, 14, 50)
			if qty > 0 && checkRiskLimits("BUY", qty) {
				orders = []api.Order{
					{Ticker: callTicker, Quantity: qty, Action: "BUY", Type: "MARKET"},
					{Ticker: putTicker, Quantity: qty, Action: "BUY", Type: "MARKET"},
				}
			}
		} else if iv-fixt > forecast.High {
			qty := chooseQty(iv-forecast.High, T, 14, 50)
			if qty > 0 && checkRiskLimits("SELL", qty) {
				orders = []api.Order{
					{Ticker: callTicker, Quantity: qty, Action: "SELL", Type: "MARKET"},
					{Ticker: putTicker, Quantity: qty, Action: "SELL", Type: "MARKET"},
				}
			}
		}
	}

	// 7. Risk limit check and send orders
	if len(orders) > 0 {
		if err := SendOrders(ctx, baseURL, apiKey, orders); err != nil {
			log.Println("order err:", err)
		} else {
			var targetIV float64
			if forecast.Type == "increase" {
				targetIV = forecast.High
			} else if forecast.Type == "decrease" {
				targetIV = forecast.Low
			} else {
				targetIV = (forecast.High + forecast.Low) / 2
			}
			AddPosition(callTicker, putTicker, orders[0].Quantity, orders[0].Action, iv, targetIV)
			updateIV(targetIV)
			log.Printf("tick %d: executed %d orders, IV=%.2f%% forecast=%s [%.2f%%-%.2f%%]\n",
				tick, len(orders), iv, forecast.Type, forecast.Low, forecast.High)
		}
	}

	// 8. Hedge delta automatically
	ManageDelta(ctx, baseURL, apiKey, S, tick, iv)
}
