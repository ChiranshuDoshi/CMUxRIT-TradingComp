package api

type Order struct {
	Ticker   string `json:"ticker"`
	Type     string `json:"type"`
	Quantity int    `json:"quantity"`
	Action   string `json:"action"`
}

type Security struct {
	Ticker           string  `json:"ticker"`
	Bid              float64 `json:"bid"`
	Ask              float64 `json:"ask"`
	Last             float64 `json:"last"`
	Currency         string  `json:"currency"`
	TradingFee       float64 `json:"trading_fee"`
	LimitOrderRebate float64 `json:"limit_order_rebate"`

	// add these to read positions/P&L from GetSecurities
	Position   float64 `json:"position"`   // current position size
	Unrealized float64 `json:"unrealized"` // unrealised P&L
	Realized   float64 `json:"realized"`   // realised P&L

	// optional if you want to look at sizes or limits:
	AskSize      float64 `json:"ask_size"`
	BidSize      float64 `json:"bid_size"`
	MaxTradeSize float64 `json:"max_trade_size"`
}

type BookLevel struct {
	Price    float64 `json:"price"`
	Quantity float64 `json:"quantity"`
}
type OrderBook struct {
	Bids []BookLevel `json:"bids"`
	Asks []BookLevel `json:"asks"`
}

type Tender struct {
	TenderID   int     `json:"tender_id"`
	Period     int     `json:"period"`
	Tick       int     `json:"tick"`
	Expires    int     `json:"expires"`
	Caption    string  `json:"caption"`
	Quantity   float64 `json:"quantity"`
	Action     string  `json:"action"` // "BUY" or "SELL"
	IsFixedBid bool    `json:"is_fixed_bid"`
	Price      float64 `json:"price"`
}

type CaseInfo struct {
	Name                   string `json:"name"`
	Period                 int    `json:"period"`
	Tick                   int    `json:"tick"`
	TicksPerPeriod         int    `json:"ticks_per_period"`
	TotalPeriods           int    `json:"total_periods"`
	Status                 string `json:"status"`
	IsEnforceTradingLimits bool   `json:"is_enforce_trading_limits"`
}

type News struct {
	NewsID   int    `json:"news_id"`
	Period   int    `json:"period"`
	Tick     int    `json:"tick"`
	Ticker   string `json:"ticker"`
	Headline string `json:"headline"`
	Body     string `json:"body"`
}
