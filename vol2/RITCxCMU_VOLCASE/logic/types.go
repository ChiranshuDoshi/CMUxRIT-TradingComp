package logic

type OptionPosition struct {
	CallTicker string
	PutTicker  string
	Quantity   int
	Action     string  // "BUY" or "SELL" when opened
	EntryIV    float64 // implied vol at entry (%)
	TargetIV   float64 // vol at which we close (%)
}

type OptionEdge struct {
	Ticker      string
	Strike      float64
	Call        bool
	MarketIV    float64
	FairIV      float64
	Vega        float64 // per contract * 100 shares
	EdgeDollars float64 // (fair - market)*100 - cost
}
