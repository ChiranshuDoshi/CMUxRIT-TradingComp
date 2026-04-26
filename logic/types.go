package logic

type OptionPosition struct {
	CallTicker string
	PutTicker  string
	Quantity   int
	Action     string  // "BUY" or "SELL" when opened
	EntryIV    float64 // implied vol at entry (%)
	TargetIV   float64 // vol at which we close (%)
}
