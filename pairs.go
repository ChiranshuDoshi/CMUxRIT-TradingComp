package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Structs to hold asset and news data
type Asset struct {
	Ticker   string  `json:"ticker"`
	Last     float64 `json:"last"`
	Size     float64 `json:"size"`
	Position float64 `json:"position"`
}

type News struct {
	Ticker   string `json:"ticker"`
	Headline string `json:"headline"`
	Body     string `json:"body"`
}

type Helper struct {
	ShareExposure   float64 `json:"share_exposure"`
	RequiredHedge   float64 `json:"required_hedge"`
	MustBeTraded    float64 `json:"must_be_traded"`
	CurrentPos      string  `json:"current_pos"`
	RequiredPos     string  `json:"required_pos"`
	SAME            bool    `json:"SAME"`
	TotalDelta      float64 `json:"total_delta"`
	RtmNetPos       float64 `json:"rtm_net_pos"`
	RtmGrossPos     float64 `json:"rtm_gross_pos"`
	OptionsNetPos   float64 `json:"options_net_pos"`
	OptionsGrossPos float64 `json:"options_gross_pos"`
}

// PositionData tracks entry price, volatility, and tick for P&L calculations
type PositionData struct {
	Size       float64
	EntryPrice float64
	EntryVol   float64
	EntryTick  int
	EntryValue float64
	HighestPnL float64 // New field to track highest P&L
}

// Global state for trading
var newsVolatilities = make(map[string]float64)
var entryPositions = make(map[string]PositionData)
var optionsNetPosition float64
var optionsGrossPosition float64
var etfNetPosition float64
var etfGrossPosition float64
var portfolioPnL float64
var portfolioValue float64
var initialPortfolioValue = 100000.0
var rtmPrices []float64

// Constants for API and trading strategy
const API_KEY = "18WWG30P"
const baseURL = "http://localhost:9999/v1"

// Trading and Risk Management Limits
const MAX_RTM_TRADE_SIZE_RATIO = 0.20
const MAX_OPTION_TRADE_SIZE_RATIO = 0.05
const DELTA_LIMIT_RATIO = 0.10
const RTM_GROSS_LIMIT = 50000.0
const RTM_NET_LIMIT = 50000.0
const OPTIONS_GROSS_LIMIT = 2500.0
const OPTIONS_NET_LIMIT = 1000.0
const PORTFOLIO_DRAWDOWN_PERCENT = 0.10
const PENALTY_RATE = 0.01

// Dynamic Trailing Stop-Loss
const TRAILING_STOP_RATIO = 0.75 // Close trade if PnL drops 25% from its peak

// Refined Position Sizing
const CAPITAL_PER_TRADE = 200.0 // Fixed capital to risk per straddle trade
const IV_MA_PERIOD = 200

// Transaction Costs
const RTM_FEE_PER_SHARE = 0.01
const OPTIONS_FEE_PER_CONTRACT = 1.00

// rtmIVs stores the history of RTM implied volatilities
var rtmIVs []float64

// getTick fetches the current tick from the API.
func getTick() (int, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/case", baseURL), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-API-Key", API_KEY)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to send tick request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return 0, fmt.Errorf("unauthorized request - invalid API key")
	}
	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return 0, fmt.Errorf("failed to decode result: %v", err)
	}
	if tickVal, exists := result["tick"]; exists {
		switch v := tickVal.(type) {
		case string:
			tick, err := strconv.Atoi(v)
			if err != nil {
				return 0, fmt.Errorf("failed to convert tick string to int: %v", err)
			}
			return tick, nil
		case float64:
			return int(v), nil
		default:
			return 0, fmt.Errorf("unexpected type for tick: %T", v)
		}
	}
	return 0, fmt.Errorf("tick field not found in the API response")
}

// getAssets fetches all securities data from the API.
func getAssets() ([]Asset, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/securities", baseURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", API_KEY)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var assets []Asset
	err = json.NewDecoder(resp.Body).Decode(&assets)
	if err != nil {
		return nil, fmt.Errorf("failed to decode assets: %v", err)
	}

	optionsNetPosition, optionsGrossPosition = 0.0, 0.0
	etfNetPosition, etfGrossPosition = 0.0, 0.0
	portfolioPnL = 0.0
	for _, asset := range assets {
		if asset.Ticker == "RTM" {
			etfNetPosition += asset.Position
			etfGrossPosition += math.Abs(asset.Position)
			entryData, ok := entryPositions[asset.Ticker]
			if ok {
				portfolioPnL += (asset.Last - entryData.EntryPrice) * asset.Position
			}
		} else {
			optionsNetPosition += asset.Position
			optionsGrossPosition += math.Abs(asset.Position)
			entryData, ok := entryPositions[asset.Ticker]
			if ok {
				pnl := (asset.Last - entryData.EntryPrice) * asset.Position * 100
				portfolioPnL += pnl
			}
		}
	}
	portfolioValue = initialPortfolioValue + portfolioPnL
	return assets, nil
}

// getNews fetches news items from the API and updates the newsVolatilities map.
func getNews() error {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/news", baseURL), nil)
	if err != nil {
		return fmt.Errorf("failed to create news request: %v", err)
	}
	req.Header.Set("X-API-Key", API_KEY)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send news request: %v", err)
	}
	defer resp.Body.Close()
	var newsItems []News
	err = json.NewDecoder(resp.Body).Decode(&newsItems)
	if err != nil {
		return fmt.Errorf("failed to decode news: %v", err)
	}
	for _, news := range newsItems {
		if strings.Contains(strings.ToUpper(news.Headline), "VOLATILITY INCREASE") {
			log.Printf("News: '%s' - detected volatility increase for %s", news.Headline, news.Ticker)
			newsVolatilities[news.Ticker] = 0.50
		} else if strings.Contains(strings.ToUpper(news.Headline), "VOLATILITY DECREASE") {
			log.Printf("News: '%s' - detected volatility decrease for %s", news.Headline, news.Ticker)
			newsVolatilities[news.Ticker] = 0.05
		}
	}
	return nil
}

// getUnderlyingPrice fetches the price of the underlying asset from the assets list.
func getUnderlyingPrice(assets []Asset) (float64, error) {
	for _, asset := range assets {
		if asset.Ticker == "RTM" {
			return asset.Last, nil
		}
	}
	return 0, fmt.Errorf("underlying asset 'RTM' not found")
}

// Black-Scholes option pricing formula
func blackScholes(optionType string, S, K, T, r, v float64) float64 {
	if v <= 0 || T <= 0 {
		return 0
	}
	d1 := (math.Log(S/K) + (r+0.5*v*v)*T) / (v * math.Sqrt(T))
	d2 := d1 - v*math.Sqrt(T)
	if optionType == "call" {
		return S*normCdf(d1) - K*math.Exp(-r*T)*normCdf(d2)
	} else if optionType == "put" {
		return K*math.Exp(-r*T)*normCdf(-d2) - S*normCdf(-d1)
	}
	return 0
}

// CDF of the standard normal distribution
func normCdf(x float64) float64 {
	return 0.5*math.Erf(x/math.Sqrt2) + 0.5
}

// Vega of the option
func vega(S, K, T, r, v float64) float64 {
	if v <= 0 || T <= 0 {
		return 0
	}
	d1 := (math.Log(S/K) + (r+0.5*v*v)*T) / (v * math.Sqrt(T))
	return S * normPdf(d1) * math.Sqrt(T)
}

// Delta of the option
func delta(optionType string, S, K, T, r, v float64) float64 {
	if v <= 0 || T <= 0 {
		return 0
	}
	d1 := (math.Log(S/K) + (r+0.5*v*v)*T) / (v * math.Sqrt(T))
	if optionType == "call" {
		return normCdf(d1)
	} else if optionType == "put" {
		return normCdf(d1) - 1.0
	}
	return 0
}

// PDF of the standard normal distribution
func normPdf(x float64) float64 {
	return math.Exp(-0.5*x*x) / math.Sqrt(2*math.Pi)
}

// Calculate implied volatility using a numerical method (Newton-Raphson)
func impliedVolatility(price, S, K, T, r float64, optionType string) float64 {
	if price <= 0 || S <= 0 || K <= 0 || T <= 0 {
		return -1.0
	}
	const tolerance = 0.0001
	const maxIterations = 100
	v := 0.2
	for i := 0; i < maxIterations; i++ {
		priceEstimate := blackScholes(optionType, S, K, T, r, v)
		vegaVal := vega(S, K, T, r, v)
		if vegaVal == 0 {
			break
		}
		v = v - (priceEstimate-price)/vegaVal
		if v < 0 {
			v = 0.001
		}
		if math.Abs(priceEstimate-price) < tolerance {
			return v
		}
	}
	return -1
}

// extractStrikePrice extracts the strike price from the ticker string.
func extractStrikePrice(ticker string) float64 {
	re := regexp.MustCompile(`(\d+)([CP])$`)
	match := re.FindStringSubmatch(ticker)
	if len(match) > 1 {
		strikePrice, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			log.Printf("Error parsing strike price for %s: %v", ticker, err)
			return 0.0
		}
		return strikePrice
	}
	return 0.0
}

// sendOrder sends a market order to the trading API and updates entry price.
func sendOrder(ticker, direction string, size int, currentPrice float64, currentTick int) error {
	if size <= 0 {
		return nil
	}

	// Apply transaction costs
	var transactionCost float64
	if ticker == "RTM" {
		transactionCost = float64(size) * RTM_FEE_PER_SHARE
	} else {
		transactionCost = float64(size) * OPTIONS_FEE_PER_CONTRACT
	}
	portfolioValue -= transactionCost

	orderURL := fmt.Sprintf("%s/orders?ticker=%s&type=MARKET&quantity=%d&action=%s", baseURL, ticker, size, direction)
	req, err := http.NewRequest("POST", orderURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create order request: %v", err)
	}
	req.Header.Set("X-API-Key", API_KEY)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send order request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned non-OK status: %s", resp.Status)
	}

	currentPos, ok := entryPositions[ticker]
	if !ok {
		entryPositions[ticker] = PositionData{
			Size:       float64(size),
			EntryPrice: currentPrice,
			EntryTick:  currentTick,
			EntryValue: currentPrice * float64(size),
			HighestPnL: 0.0,
		}
	} else {
		tradedSize := float64(size)
		if direction == "SELL" {
			tradedSize = -tradedSize
		}
		newSize := currentPos.Size + tradedSize

		if math.Abs(newSize) < 0.01 {
			delete(entryPositions, ticker)
			log.Printf("Position for %s closed.", ticker)
		} else {
			currentTotalValue := currentPos.EntryPrice * currentPos.Size
			tradedTotalValue := currentPrice * tradedSize
			newEntryPrice := (currentTotalValue + tradedTotalValue) / newSize

			currentPos.Size = newSize
			currentPos.EntryPrice = newEntryPrice
			currentPos.EntryTick = currentTick
			currentPos.EntryValue += currentPrice * float64(size)
			entryPositions[ticker] = currentPos
		}
	}

	log.Printf("Order for %s %s successful. Size: %d, New Position: %.0f. Cost: %.2f", ticker, direction, size, entryPositions[ticker].Size, transactionCost)
	return nil
}

// getAssetsSync is a helper to get assets for other functions
func getAssetsSync() []Asset {
	assets, err := getAssets()
	if err != nil {
		log.Printf("Error getting assets in getAssetsSync: %v", err)
		return nil
	}
	return assets
}

// findATMStrike finds the closest at-the-money strike price
func findATMStrike(assets []Asset, underlyingPrice float64) (float64, error) {
	minDiff := math.MaxFloat64
	atmStrike := 0.0
	found := false
	for _, asset := range assets {
		if strings.HasSuffix(asset.Ticker, "C") || strings.HasSuffix(asset.Ticker, "P") {
			strike := extractStrikePrice(asset.Ticker)
			if math.Abs(strike-underlyingPrice) < minDiff {
				minDiff = math.Abs(strike - underlyingPrice)
				atmStrike = strike
				found = true
			}
		}
	}
	if !found {
		return 0, fmt.Errorf("no options found to determine ATM strike")
	}
	return atmStrike, nil
}

// This function now implements the pairs trading strategy by maintaining delta neutrality.
func handleAssets(assets []Asset, currentTick int) ([]Asset, Helper) {
	var helper Helper
	underlyingPrice, err := getUnderlyingPrice(assets)
	if err != nil {
		log.Printf("Error getting underlying price: %v", err)
		return assets, helper
	}

	totalDelta := 0.0
	rtmPosition := 0.0
	rtmGross := 0.0
	optionsGross := 0.0

	for _, asset := range assets {
		if asset.Ticker == "RTM" {
			rtmPosition = asset.Position
			rtmGross += math.Abs(asset.Position)
		} else {
			optionsGross += math.Abs(asset.Position)
			r, T, iVol := 0.0, 1.0, 0.20
			strikePrice := extractStrikePrice(asset.Ticker)
			optionType := optionType(asset.Ticker)

			currentImpliedVol := impliedVolatility(asset.Last, underlyingPrice, strikePrice, T, r, optionType)
			if currentImpliedVol > 0 {
				iVol = currentImpliedVol
			}

			// Add to total delta, considering 1 contract = 100 shares
			totalDelta += delta(optionType, underlyingPrice, strikePrice, T, r, iVol) * asset.Position * 100
		}
	}

	totalDelta += rtmPosition
	helper.TotalDelta = totalDelta
	helper.RtmGrossPos = rtmGross
	helper.OptionsGrossPos = optionsGross
	helper.RtmNetPos = rtmPosition
	helper.OptionsNetPos = optionsNetPosition

	// --- Dynamic Straddle Management Logic ---
	var openCallAsset, openPutAsset Asset
	straddleOpen := false
	for _, asset := range assets {
		if strings.Contains(asset.Ticker, "C") && asset.Position > 0 {
			openCallAsset = asset
			straddleOpen = true
		}
		if strings.Contains(asset.Ticker, "P") && asset.Position > 0 {
			openPutAsset = asset
			straddleOpen = true
		}
	}

	if straddleOpen {
		// Correctly calculate P&L for the entire straddle
		callEntryData, callOk := entryPositions[openCallAsset.Ticker]
		putEntryData, putOk := entryPositions[openPutAsset.Ticker]

		if callOk && putOk {
			currentValue := (openCallAsset.Last * openCallAsset.Position * 100) + (openPutAsset.Last * openPutAsset.Position * 100)
			entryValue := (callEntryData.EntryPrice * callEntryData.Size * 100) + (putEntryData.EntryPrice * putEntryData.Size * 100)
			totalStraddlePnL := currentValue - entryValue

			// Update highest PnL for trailing stop-loss
			if totalStraddlePnL > callEntryData.HighestPnL {
				callEntryData.HighestPnL = totalStraddlePnL
				entryPositions[openCallAsset.Ticker] = callEntryData
			}

			// Check for trailing stop-loss
			if totalStraddlePnL < callEntryData.HighestPnL*TRAILING_STOP_RATIO {
				log.Printf("TRAILING STOP-LOSS HIT: Straddle P&L fell %.2f%% from its peak. Squaring off.", (1-TRAILING_STOP_RATIO)*100)
				sendOrder(openCallAsset.Ticker, "SELL", int(math.Abs(openCallAsset.Position)), openCallAsset.Last, currentTick)
				sendOrder(openPutAsset.Ticker, "SELL", int(math.Abs(openPutAsset.Position)), openPutAsset.Last, currentTick)
				return assets, helper
			}
		}
	} else {
		// Open a new straddle to initiate the "pair" if none exists
		atmStrike, err := findATMStrike(assets, underlyingPrice)
		if err != nil {
			log.Printf("Could not find ATM strike to open straddle: %v", err)
		} else {
			atmCallTicker := fmt.Sprintf("RTM%dC", int(atmStrike))
			atmPutTicker := fmt.Sprintf("RTM%dP", int(atmStrike))
			var atmCallAsset, atmPutAsset Asset
			for _, asset := range assets {
				if asset.Ticker == atmCallTicker {
					atmCallAsset = asset
				}
				if asset.Ticker == atmPutTicker {
					atmPutAsset = asset
				}
			}

			// We need to check if the asset data is valid before using it
			if atmCallAsset.Last == 0 || atmPutAsset.Last == 0 {
				log.Println("Could not find current prices for ATM options. Skipping trade.")
				return assets, helper
			}

			// Use fixed capital per trade for position sizing
			// This is a more robust way to control risk
			tradeSize := int(CAPITAL_PER_TRADE / (atmCallAsset.Last + atmPutAsset.Last))

			// Open a new straddle only if optionsGross is 0 and tradeSize is positive
			if optionsGross == 0 && tradeSize > 0 {
				if optionsGross+float64(tradeSize*2) > OPTIONS_GROSS_LIMIT || math.Abs(optionsNetPosition+float64(tradeSize*2)) > OPTIONS_NET_LIMIT {
					log.Println("Cannot open new straddle, would exceed options gross/net limits.")
				} else {
					log.Printf("Opening new ATM straddle at strike %d with size %d.", int(atmStrike), tradeSize)
					sendOrder(atmCallTicker, "BUY", tradeSize, atmCallAsset.Last, currentTick)
					sendOrder(atmPutTicker, "BUY", tradeSize, atmPutAsset.Last, currentTick)
				}
			}
		}
	}

	// --- Pairs Trading (Delta Hedging) Logic ---
	// Trade RTM to bring the portfolio's delta back toward the center (0)
	deltaLimit := DELTA_LIMIT_RATIO * portfolioValue
	maxRTMTradeSize := int(MAX_RTM_TRADE_SIZE_RATIO * portfolioValue)

	if totalDelta > deltaLimit {
		hedgeSize := totalDelta - deltaLimit
		if hedgeSize > float64(maxRTMTradeSize) {
			hedgeSize = float64(maxRTMTradeSize)
		}
		if rtmGross+hedgeSize > RTM_GROSS_LIMIT || rtmPosition-hedgeSize < -RTM_NET_LIMIT {
			log.Println("Cannot perform hedge, would exceed RTM gross/net limits.")
		} else {
			log.Printf("PAIRS TRADING: Total Delta: %.2f. Selling RTM to reduce long exposure. Size: %.0f", totalDelta, hedgeSize)
			sendOrder("RTM", "SELL", int(hedgeSize), underlyingPrice, currentTick)
		}
	} else if totalDelta < -deltaLimit {
		hedgeSize := math.Abs(totalDelta + deltaLimit)
		if hedgeSize > float64(maxRTMTradeSize) {
			hedgeSize = float64(maxRTMTradeSize)
		}
		if rtmGross+hedgeSize > RTM_GROSS_LIMIT || rtmPosition+hedgeSize > RTM_NET_LIMIT {
			log.Println("Cannot perform hedge, would exceed RTM gross/net limits.")
		} else {
			log.Printf("PAIRS TRADING: Total Delta: %.2f. Buying RTM to reduce short exposure. Size: %.0f", totalDelta, hedgeSize)
			sendOrder("RTM", "BUY", int(hedgeSize), underlyingPrice, currentTick)
		}
	} else {
		log.Println("Portfolio is within delta limits. No pairs trade required.")
	}

	return assets, helper
}

// optionType determines if the ticker is a "call" or a "put"
func optionType(ticker string) string {
	if len(ticker) > 0 {
		if ticker[len(ticker)-1] == 'C' {
			return "call"
		} else if ticker[len(ticker)-1] == 'P' {
			return "put"
		}
	}
	return ""
}

func main() {
	for {
		log.Println("Starting new trading cycle...")
		tick, err := getTick()
		if err != nil {
			log.Printf("Error getting tick: %v. Retrying...", err)
			time.Sleep(2 * time.Second)
			continue
		}
		fmt.Printf("Current tick: %d\n", tick)
		err = getNews()
		if err != nil {
			log.Printf("Error getting news: %v. Using previous news data...", err)
		}

		assets, err := getAssets()
		if err != nil {
			log.Printf("Error getting assets: %v. Retrying...", err)
			time.Sleep(2 * time.Second)
			continue
		}

		underlyingPrice, err := getUnderlyingPrice(assets)
		if err != nil {
			log.Printf("Error getting underlying price for delta calculation: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		// Calculate penalty for this second
		totalDelta := 0.0
		for _, asset := range assets {
			if asset.Ticker == "RTM" {
				totalDelta += asset.Position
			} else {
				r, T, iVol := 0.0, 1.0, 0.20
				strikePrice := extractStrikePrice(asset.Ticker)
				optionType := optionType(asset.Ticker)
				currentImpliedVol := impliedVolatility(asset.Last, underlyingPrice, strikePrice, T, r, optionType)
				if currentImpliedVol > 0 {
					iVol = currentImpliedVol
				}
				totalDelta += delta(optionType, underlyingPrice, strikePrice, T, r, iVol) * asset.Position * 100
			}
		}

		deltaLimit := DELTA_LIMIT_RATIO * portfolioValue
		if math.Abs(totalDelta) > deltaLimit {
			penalty := (math.Abs(totalDelta) - deltaLimit) * PENALTY_RATE
			portfolioValue -= penalty
			log.Printf("DELTA LIMIT EXCEEDED! Current Delta: %.2f. Penalty incurred: $%.2f", totalDelta, penalty)
		}

		if portfolioValue < initialPortfolioValue*(1-PORTFOLIO_DRAWDOWN_PERCENT) {
			log.Printf("PORTFOLIO STOP-LOSS HIT! Portfolio Value: %.2f, Drawdown: %.2f%%. Squaring off all positions.", portfolioValue, PORTFOLIO_DRAWDOWN_PERCENT*100)
			for _, asset := range assets {
				if asset.Position != 0 {
					direction := "SELL"
					if asset.Position < 0 {
						direction = "BUY"
					}
					sendOrder(asset.Ticker, direction, int(math.Abs(asset.Position)), asset.Last, tick)
				}
			}
			return
		}

		_, helper := handleAssets(assets, tick)
		for _, asset := range assets {
			entryData, ok := entryPositions[asset.Ticker]
			pnlString := "N/A"
			if ok {
				pnl := (asset.Last - entryData.EntryPrice) * asset.Position
				if asset.Ticker != "RTM" {
					pnl *= 100
				}
				pnlString = fmt.Sprintf("%.2f", pnl)
			}
			fmt.Printf("Asset: %s, Last Price: %.2f, Position: %.0f, P&L: %s\n", asset.Ticker, asset.Last, asset.Position, pnlString)
		}
		fmt.Printf("Helper Data: %+v\n", helper)
		fmt.Printf("Current Portfolio Value: %.2f\n", portfolioValue)
		log.Println("Trading cycle complete. Waiting 2 seconds before next cycle.")
		time.Sleep(2 * time.Second)
	}
}
