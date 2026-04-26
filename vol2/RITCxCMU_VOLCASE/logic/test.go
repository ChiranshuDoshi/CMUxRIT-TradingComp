package logic

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"volcase/api"
)

// BlackScholesGreeks returns price, delta, vega (per share) for r=0.
func BlackScholesGreeks(S, K, T, sigma float64, isCall bool) (price, delta, vega float64) {
	if T <= 0 || sigma <= 0 {
		// intrinsic only
		if isCall {
			price = math.Max(0, S-K)
			delta = 0
		} else {
			price = math.Max(0, K-S)
			delta = 0
		}
		vega = 0
		return
	}
	sqrtT := math.Sqrt(T)
	d1 := (math.Log(S/K) + 0.5*sigma*sigma*T) / (sigma * sqrtT)
	d2 := d1 - sigma*sqrtT

	if isCall {
		price = S*normCDF(d1) - K*normCDF(d2) // r=0
		delta = normCDF(d1)
	} else {
		price = K*normCDF(-d2) - S*normCDF(-d1)
		delta = normCDF(d1) - 1.0
	}
	pdf := math.Exp(-0.5*d1*d1) / math.Sqrt(2*math.Pi)
	vega = S * pdf * sqrtT // per 1.00 (100%) vol, per share
	return
}

// computeEdge compares market mid vs your fair (analyst) sigma.
// analystSigma should be DECIMAL (e.g., 0.22), marketMid is $.
func computeEdge(S, K, T, analystSigma float64, marketMid float64, isCall bool) OptionEdge {
	fairPrice, _, vegaPerShare := BlackScholesGreeks(S, K, T, analystSigma, isCall)
	mktIV := ImpliedVol(marketMid, S, K, T, 0.0, isCall) // decimal
	vegaPerContract := vegaPerShare * 100.0

	// crude expected edge after $1 commission and 1c half-spread*100
	edge := (fairPrice-marketMid)*100.0 - 1.0 - 1.0 // $1 fee + ~$1 spread per contract
	if edge < 0 {
		edge = 0
	}

	ticker := fmt.Sprintf("RTM%.0f%s", K, map[bool]string{true: "C", false: "P"}[isCall])
	return OptionEdge{
		Ticker:      ticker,
		Strike:      K,
		Call:        isCall,
		MarketIV:    mktIV,
		FairIV:      analystSigma,
		Vega:        vegaPerContract,
		EdgeDollars: edge,
	}
}

// getMid returns top-of-book mid or false if unavailable.
func getMid(ctx context.Context, baseURL, apiKey, ticker string) (float64, bool) {
	ob, err := api.GetOrderBook(ctx, baseURL, apiKey, ticker)
	if err != nil || len(ob.Bids) == 0 || len(ob.Asks) == 0 {
		return 0, false
	}
	return (ob.Bids[0].Price + ob.Asks[0].Price) / 2.0, true
}

// pickBestATMEdge scans calls+puts across the 5 strikes and returns the best edge.
func pickBestATMEdge(ctx context.Context, baseURL, apiKey string, S, T, analystSigma float64) (OptionEdge, bool) {
	allowed := []float64{48, 49, 50, 51, 52}
	best := OptionEdge{EdgeDollars: -1}
	found := false

	for _, K := range allowed {
		callTicker := fmt.Sprintf("RTM%.0fC", K)
		if mid, ok := getMid(ctx, baseURL, apiKey, callTicker); ok {
			e := computeEdge(S, K, T, analystSigma, mid, true)
			// prefer higher EdgeDollars; break ties by higher Vega (ATM bias)
			if e.EdgeDollars > best.EdgeDollars || (e.EdgeDollars == best.EdgeDollars && e.Vega > best.Vega) {
				best = e
				found = true
			}
		}
		putTicker := fmt.Sprintf("RTM%.0fP", K)
		if mid, ok := getMid(ctx, baseURL, apiKey, putTicker); ok {
			e := computeEdge(S, K, T, analystSigma, mid, false)
			if e.EdgeDollars > best.EdgeDollars || (e.EdgeDollars == best.EdgeDollars && e.Vega > best.Vega) {
				best = e
				found = true
			}
		}
	}
	return best, found
}

// decideATMAction returns BUY if market IV < fair (cheap), else SELL (rich).
func decideATMAction(edge OptionEdge) string {
	if edge.MarketIV < edge.FairIV {
		return "BUY"
	}
	return "SELL"
}

// chooseATMQty: use your net/gross limits; also cap at 100 per order.
func chooseATMQty(action string, minLot, maxPerOrder int) int {
	// How many contracts per leg we can still add by your limits:
	room := maxAddableContracts(action) // returns per-leg capacity for a straddle; we are single-leg, so it's fine.
	if room <= 0 {
		return 0
	}
	if room < minLot {
		return room
	}
	if room > maxPerOrder {
		return maxPerOrder
	}
	return room
}

// (Optional) build a same-type vertical to cut delta if the chosen strike is not ATM.
func buildVertical(ctx context.Context, baseURL, apiKey string, chosen OptionEdge, action string) (api.Order, *api.Order, bool) {
	// pick neighbor strike toward ATM to short against if it exists
	var neighborStrike float64
	if chosen.Call {
		// if we bought/sold a high strike, use the next lower; if low strike, next higher.
		if chosen.Strike > chosen.Strike-1 { // always true; we just go one step toward S
			neighborStrike = chosen.Strike - 1
		}
	} else {
		neighborStrike = chosen.Strike + 1
	}
	if neighborStrike < 48 || neighborStrike > 52 {
		// no neighbor within listed set
		o := api.Order{Ticker: chosen.Ticker, Quantity: 0, Action: action, Type: "MARKET"}
		return o, nil, false
	}

	otherTicker := fmt.Sprintf("RTM%.0f%s", neighborStrike, map[bool]string{true: "C", false: "P"}[chosen.Call])
	// Opposite direction on the neighbor to create a vertical
	contra := OppositeAction(action)
	return api.Order{Ticker: chosen.Ticker, Action: action, Type: "MARKET"},
		&api.Order{Ticker: otherTicker, Action: contra, Type: "MARKET"},
		true
}

// RunATMEdgeStrategy picks the best ATM-ish leg and trades it, then hedges with ETF.
// analystSigmaPct is in PERCENT (e.g., 22.0)
func RunATMEdgeStrategy(ctx context.Context, baseURL, apiKey string, S float64, tick int, analystSigmaPct float64) {
	T := CalcT(tick)
	analystSigma := analystSigmaPct / 100.0

	edge, ok := pickBestATMEdge(ctx, baseURL, apiKey, S, T, analystSigma)
	if !ok || edge.EdgeDollars <= 0 {
		return
	}
	action := decideATMAction(edge)

	// size
	qty := chooseATMQty(action, 50, 100) // try 50–100 per clip; adjust to taste
	if qty <= 0 {
		return
	}

	// build orders (single-leg by default)
	order := api.Order{Ticker: edge.Ticker, Quantity: qty, Action: action, Type: "MARKET"}
	orders := []api.Order{order}

	// OPTIONAL: add neighbor leg to form a vertical to tame delta (comment in if desired)
	// if false { // set to true to enable vertical
	// 	o1, o2, okV := buildVertical(ctx, baseURL, apiKey, edge, action)
	// 	if okV {
	// 		o1.Quantity = qty
	// 		o2.Quantity = qty
	// 		orders = []api.Order{o1, *o2}
	// 	}
	// }

	// risk check using your existing function
	if !checkRiskLimits(action, qty) {
		return
	}

	// send orders
	if err := SendOrders(ctx, baseURL, apiKey, orders); err != nil {
		log.Println("ATM-edge order err:", err)
		return
	}
	log.Printf("[ATM-Vega] %s %d x %s (mkt IV %.2f%%, fair IV %.2f%%, vega/ct %.2f, edge $%.2f)\n",
		action, qty, edge.Ticker, edge.MarketIV*100, edge.FairIV*100, edge.Vega, edge.EdgeDollars)

	// immediate delta management using your hedger
	ManageDelta(ctx, baseURL, apiKey, S, tick, edge.MarketIV*100.0) // pass IV% for your ManageDelta signature
}

// straddleMids gets current mids for a given strike's call & put.
func straddleMids(ctx context.Context, baseURL, apiKey string, strike int) (midC, midP float64, ok bool) {
	callTicker := fmt.Sprintf("RTM%dC", strike)
	putTicker := fmt.Sprintf("RTM%dP", strike)

	getMid := func(t string) (float64, bool) {
		ob, e := api.GetOrderBook(ctx, baseURL, apiKey, t)
		if e != nil || len(ob.Bids) == 0 || len(ob.Asks) == 0 {
			return 0, false
		}
		return (ob.Bids[0].Price + ob.Asks[0].Price) / 2.0, true
	}
	var ok1, ok2 bool
	midC, ok1 = getMid(callTicker)
	midP, ok2 = getMid(putTicker)
	return midC, midP, ok1 && ok2
}

// straddleEdgeDollars after fees/spread against your analyst sigma (decimal).
// Positive -> long straddle has positive edge; negative -> short straddle edge.
func straddleEdgeDollars(S, K, T, analystSigma float64, midC, midP float64) float64 {
	// Fair prices under analyst sigma
	fairC := BlackScholesPrice(S, K, T, 0.0, analystSigma, true)
	fairP := BlackScholesPrice(S, K, T, 0.0, analystSigma, false)

	// After-costs (approx $1 fee + $1 spread per contract)
	edgeCall := (fairC-midC)*100.0 - 2.0
	edgePut := (fairP-midP)*100.0 - 2.0

	// Long straddle edge = edgeCall + edgePut (if positive, buy has edge)
	return edgeCall + edgePut
}

func closeStraddlePortion(ctx context.Context, baseURL, apiKey string, pos OptionPosition, qty int) bool {
	if qty <= 0 {
		return true
	}
	action := OppositeAction(pos.Action) // reverse the original
	// clip into ≤100 per order
	for qty > 0 {
		clip := qty
		if clip > 100 {
			clip = 100
		}
		orders := []api.Order{
			{Ticker: pos.CallTicker, Quantity: clip, Action: action, Type: "MARKET"},
			{Ticker: pos.PutTicker, Quantity: clip, Action: action, Type: "MARKET"},
		}
		if err := SendOrders(ctx, baseURL, apiKey, orders); err != nil {
			log.Println("close err:", err)
			return false
		}
		qty -= clip
	}
	// Reduce or remove from openPositions
	for i := range openPositions {
		if openPositions[i].CallTicker == pos.CallTicker &&
			openPositions[i].PutTicker == pos.PutTicker &&
			strings.EqualFold(openPositions[i].Action, pos.Action) {
			openPositions[i].Quantity -= qty // qty already zero here; defensive
			if openPositions[i].Quantity <= 0 {
				RemovePosition(pos)
			}
			break
		}
	}
	return true
}

// BookProfits evaluates each open straddle and exits when:
// - target IV reached (with hysteresis), or
// - edge shrinks below threshold, or
// - very late in heat (tick ≥ 295), or
// - edge flips against us.
// It hedges after closing to avoid residual ETF exposure.
func BookProfits(ctx context.Context, baseURL, apiKey string, S float64, tick int, analystSigmaPct float64) {
	if len(openPositions) == 0 {
		return
	}

	T := CalcT(tick)
	analystSigma := analystSigmaPct / 100.0

	// thresholds
	const ivHysteresis = 0.2 // 0.2 vol points (percent) around target
	const minEdgeTake = 1.0  // $ per straddle after-cost to keep; below → scale/exit
	const lateTick = 295     // hard flatten near end
	const scaleOutFrac = 0.5 // scale out 50% when edge diminished

	var toClose []struct {
		pos OptionPosition
		qty int
	}
	for _, pos := range openPositions {
		strike := strikeFromTicker(pos.CallTicker)
		midC, midP, ok := straddleMids(ctx, baseURL, apiKey, strike)
		if !ok {
			continue
		}

		edge := straddleEdgeDollars(S, float64(strike), T, analystSigma, midC, midP) // long-edge measure

		// Rule A: target IV met (use hysteresis)
		target := pos.TargetIV
		ivOK := false
		if strings.ToUpper(pos.Action) == "BUY" {
			ivOK = analystSigmaPct >= (target - ivHysteresis)
		} else {
			ivOK = analystSigmaPct <= (target + ivHysteresis)
		}

		// Rule B: edge faded/flip
		edgeFlipAgainst := (strings.ToUpper(pos.Action) == "BUY" && edge <= 0) ||
			(strings.ToUpper(pos.Action) == "SELL" && edge >= 0)
		edgeTooSmall := math.Abs(edge) < minEdgeTake

		// Rule C: very late in heat
		late := tick >= lateTick

		closeQty := 0
		switch {
		case late:
			closeQty = pos.Quantity // full exit
		case ivOK:
			closeQty = pos.Quantity // full exit on hitting target
		case edgeFlipAgainst:
			closeQty = pos.Quantity // protect P&L
		case edgeTooSmall:
			closeQty = int(math.Max(1, math.Floor(float64(pos.Quantity)*scaleOutFrac))) // partial
		default:
			// keep running
		}

		if closeQty > 0 {
			toClose = append(toClose, struct {
				pos OptionPosition
				qty int
			}{pos, closeQty})
		}
	}

	// Execute closes and re-hedge after each batch
	for _, c := range toClose {
		ok := closeStraddlePortion(ctx, baseURL, apiKey, c.pos, c.qty)
		if ok {
			log.Printf("Booked profit: closed %d of %s/%s (action=%s, targetIV=%.2f, analystIV=%.2f)\n",
				c.qty, c.pos.CallTicker, c.pos.PutTicker, c.pos.Action, c.pos.TargetIV, analystSigmaPct)
			// Hedge immediately after changes
			ManageDelta(ctx, baseURL, apiKey, S, tick, analystSigmaPct)
		}
	}
}
