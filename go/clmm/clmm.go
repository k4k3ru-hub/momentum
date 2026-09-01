package clmm

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	onchainSui "github.com/k4k3ru-hub/onchain/go/sui"
)

type Deployment struct {
	Package      onchainSui.Address
	PublishedAt  onchainSui.Address
	Version      onchainSui.ObjectInput
	Clock        onchainSui.ObjectInput
	TradeModule  string
	EventsModule string
}

// Validate validates a Momentum CLMM deployment.
//
// Returns:
//   - Validation error.
//
// Version:
//   - 2026-08-30: Added.
func (d Deployment) Validate() error {
	if d.Package.IsZero() || d.PublishedAt.IsZero() {
		return fmt.Errorf("failed to validate momentum clmm deployment: package=empty")
	}
	if d.Version.Address.IsZero() || d.Version.Version == 0 {
		return fmt.Errorf("failed to validate momentum clmm deployment: version=invalid")
	}
	if d.Clock.Address.IsZero() || d.Clock.Version == 0 {
		return fmt.Errorf("failed to validate momentum clmm deployment: clock=invalid")
	}
	if strings.TrimSpace(d.TradeModule) == "" || strings.TrimSpace(d.EventsModule) == "" {
		return fmt.Errorf("failed to validate momentum clmm deployment: module=empty")
	}
	return nil
}

type Pool struct {
	Address        onchainSui.Address
	InitialVersion uint64
	CoinTypeX      string
	CoinTypeY      string
	ReserveX       uint64
	ReserveY       uint64
	SqrtPrice      *big.Int
	Liquidity      *big.Int
	TickIndex      int32
	FeeRate        uint64
}

// ParsePool parses a Momentum CLMM pool Move object.
//
// Parameters:
//   - object: Sui pool object.
//
// Returns:
//   - Parsed pool.
//   - Parse error.
//
// Version:
//   - 2026-09-01: Parsed the deployed pool's sqrt_price, tick_index, and swap_fee_rate fields.
//   - 2026-08-30: Added.
func ParsePool(object *onchainSui.Object) (*Pool, error) {
	if object == nil || object.Move == nil {
		return nil, fmt.Errorf("failed to parse momentum clmm pool: object=null")
	}
	types, err := moveTypeArguments(object.Move.Type)
	if err != nil || len(types) != 2 || !strings.Contains(object.Move.Type, "::pool::Pool<") {
		return nil, fmt.Errorf("failed to parse momentum clmm pool: move_type=invalid")
	}
	var value struct {
		ReserveX  json.RawMessage `json:"reserve_x"`
		ReserveY  json.RawMessage `json:"reserve_y"`
		SqrtPrice json.RawMessage `json:"sqrt_price"`
		Liquidity json.RawMessage `json:"liquidity"`
		Tick      struct {
			Bits json.RawMessage `json:"bits"`
		} `json:"tick_index"`
		FeeRate json.RawMessage `json:"swap_fee_rate"`
	}
	if err := json.Unmarshal(object.Move.JSON, &value); err != nil {
		return nil, fmt.Errorf("failed to parse momentum clmm pool: failed to decode object: %w", err)
	}
	x, err := jsonUint64(value.ReserveX)
	if err != nil {
		return nil, fmt.Errorf("failed to parse momentum clmm pool: reserve_x=invalid")
	}
	y, err := jsonUint64(value.ReserveY)
	if err != nil {
		return nil, fmt.Errorf("failed to parse momentum clmm pool: reserve_y=invalid")
	}
	sqrt, err := jsonUnsigned(value.SqrtPrice)
	if err != nil {
		return nil, fmt.Errorf("failed to parse momentum clmm pool: sqrt_price=invalid")
	}
	liquidity, err := jsonUnsigned(value.Liquidity)
	if err != nil {
		return nil, fmt.Errorf("failed to parse momentum clmm pool: liquidity=invalid")
	}
	fee, err := jsonUint64(value.FeeRate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse momentum clmm pool: swap_fee_rate=invalid")
	}
	tickBits, err := jsonUint64(value.Tick.Bits)
	if err != nil || tickBits > uint64(^uint32(0)) {
		return nil, fmt.Errorf("failed to parse momentum clmm pool: tick_index=invalid")
	}
	return &Pool{Address: object.Address, InitialVersion: object.Version, CoinTypeX: types[0], CoinTypeY: types[1], ReserveX: x, ReserveY: y, SqrtPrice: sqrt, Liquidity: liquidity, TickIndex: int32(uint32(tickBits)), FeeRate: fee}, nil
}

type Simulator interface {
	SimulateTransaction(context.Context, onchainSui.SimulationRequest) (*onchainSui.SimulationResult, error)
}
type QuoteExactInputParams struct {
	Sender         onchainSui.Address
	Pool           Pool
	AmountIn       uint64
	XForY          bool
	SqrtPriceLimit *big.Int
}
type QuoteExactOutputParams struct {
	Sender         onchainSui.Address
	Pool           Pool
	AmountOut      uint64
	XForY          bool
	SqrtPriceLimit *big.Int
}
type QuoteResult struct {
	AmountIn   uint64
	AmountOut  uint64
	Checkpoint onchainSui.CheckpointSequenceNumber
}
type Quoter struct {
	deployment Deployment
	simulator  Simulator
}

// NewQuoter creates a simulation-backed Momentum CLMM quoter.
//
// Parameters:
//   - deployment: Momentum deployment.
//   - simulator: Sui simulator.
//
// Returns:
//   - Quoter.
//   - Construction error.
//
// Version:
//   - 2026-08-30: Added.
func NewQuoter(deployment Deployment, simulator Simulator) (*Quoter, error) {
	if err := deployment.Validate(); err != nil {
		return nil, fmt.Errorf("failed to create momentum clmm quoter: %w", err)
	}
	if simulator == nil {
		return nil, fmt.Errorf("failed to create momentum clmm quoter: simulator=null")
	}
	return &Quoter{deployment: deployment, simulator: simulator}, nil
}

// QuoteExactInput simulates Momentum trade::compute_swap_result.
//
// Parameters:
//   - ctx: Request context.
//   - params: Quote parameters.
//
// Returns:
//   - Quote result.
//   - Quote error.
//
// Version:
//   - 2026-09-01: Returned the simulation checkpoint.
//   - 2026-08-30: Added.
func (q *Quoter) QuoteExactInput(ctx context.Context, params QuoteExactInputParams) (QuoteResult, error) {
	if q == nil || q.simulator == nil {
		return QuoteResult{}, fmt.Errorf("failed to quote momentum clmm exact input: quoter=null")
	}
	if params.Sender.IsZero() || params.Pool.Address.IsZero() || params.Pool.InitialVersion == 0 || params.AmountIn == 0 || !validU128(params.SqrtPriceLimit) {
		return QuoteResult{}, fmt.Errorf("failed to quote momentum clmm exact input: parameters=invalid")
	}
	amountOut, checkpoint, err := q.quote(ctx, params.Sender, params.Pool, params.AmountIn, params.XForY, true, params.SqrtPriceLimit)
	if err != nil {
		return QuoteResult{}, fmt.Errorf("failed to quote momentum clmm exact input: %w", err)
	}
	return QuoteResult{AmountIn: params.AmountIn, AmountOut: amountOut, Checkpoint: checkpoint}, nil
}

// QuoteExactOutput simulates Momentum trade::compute_swap_result for an exact output.
//
// Parameters:
//   - ctx: Request context.
//   - params: Quote parameters.
//
// Returns:
//   - Quote result.
//   - Quote error.
//
// Version:
//   - 2026-09-01: Returned the simulation checkpoint.
//   - 2026-09-01: Added.
func (q *Quoter) QuoteExactOutput(ctx context.Context, params QuoteExactOutputParams) (QuoteResult, error) {
	if q == nil || q.simulator == nil {
		return QuoteResult{}, fmt.Errorf("failed to quote momentum clmm exact output: quoter=null")
	}
	if params.Sender.IsZero() || params.Pool.Address.IsZero() || params.Pool.InitialVersion == 0 || params.AmountOut == 0 || !validU128(params.SqrtPriceLimit) {
		return QuoteResult{}, fmt.Errorf("failed to quote momentum clmm exact output: parameters=invalid")
	}
	amountIn, checkpoint, err := q.quote(ctx, params.Sender, params.Pool, params.AmountOut, params.XForY, false, params.SqrtPriceLimit)
	if err != nil {
		return QuoteResult{}, fmt.Errorf("failed to quote momentum clmm exact output: %w", err)
	}
	return QuoteResult{AmountIn: amountIn, AmountOut: params.AmountOut, Checkpoint: checkpoint}, nil
}

func (q *Quoter) quote(ctx context.Context, sender onchainSui.Address, poolState Pool, amountSpecified uint64, xForY, exactInput bool, sqrtPriceLimit *big.Int) (uint64, onchainSui.CheckpointSequenceNumber, error) {
	b := onchainSui.NewProgrammableTransactionBuilder()
	pool, err := b.Object(onchainSui.InputKindShared, onchainSui.ObjectInput{Address: poolState.Address, Version: poolState.InitialVersion})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to build momentum clmm quote: %w", err)
	}
	direction, _ := b.Pure(bcsBool(xForY))
	exact, _ := b.Pure(bcsBool(exactInput))
	limit, _ := b.Pure(bcsUint128(sqrtPriceLimit))
	amount, _ := b.Pure(bcsUint64(amountSpecified))
	state, err := b.MoveCall(onchainSui.MoveCall{Package: q.deployment.PublishedAt, Module: q.deployment.TradeModule, Function: "compute_swap_result", TypeArguments: []string{poolState.CoinTypeX, poolState.CoinTypeY}, Arguments: []onchainSui.Argument{pool, direction, exact, limit, amount}})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to build momentum clmm quote: %w", err)
	}
	_, err = b.MoveCall(onchainSui.MoveCall{Package: q.deployment.PublishedAt, Module: q.deployment.TradeModule, Function: "get_state_amount_calculated", Arguments: []onchainSui.Argument{state}})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to build momentum clmm quote: %w", err)
	}
	tx, err := b.Build()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to build momentum clmm quote: %w", err)
	}
	simulation, err := q.simulator.SimulateTransaction(ctx, onchainSui.SimulationRequest{Sender: sender, Transaction: tx})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to simulate momentum clmm quote: %w", err)
	}
	if len(simulation.CommandResults) < 2 || len(simulation.CommandResults[1].ReturnValues) == 0 || len(simulation.CommandResults[1].ReturnValues[0].BCS) < 8 {
		return 0, 0, fmt.Errorf("failed to decode momentum clmm quote: command_result=invalid")
	}
	amountCalculated := binary.LittleEndian.Uint64(simulation.CommandResults[1].ReturnValues[0].BCS[:8])
	if amountCalculated == 0 {
		return 0, 0, fmt.Errorf("failed to decode momentum clmm quote: amount_calculated=empty")
	}
	return amountCalculated, simulation.Checkpoint, nil
}

type FlashSwap struct {
	Pool     onchainSui.Argument
	Version  onchainSui.Argument
	BalanceX onchainSui.Argument
	BalanceY onchainSui.Argument
	Receipt  onchainSui.Argument
}

// AppendFlashSwap appends a Momentum flash swap to a programmable transaction.
//
// Parameters:
//   - builder: Programmable transaction builder.
//   - deployment: Momentum deployment.
//   - pool: Momentum pool.
//   - xForY: Swap direction.
//   - amount: Exact input amount.
//   - sqrtPriceLimit: Q64.64 price limit.
//
// Returns:
//   - Flash-swap arguments.
//   - Validation error.
//
// Version:
//   - 2026-08-30: Added.
func AppendFlashSwap(builder *onchainSui.ProgrammableTransactionBuilder, deployment Deployment, pool Pool, xForY bool, amount uint64, sqrtPriceLimit *big.Int) (FlashSwap, error) {
	if builder == nil {
		return FlashSwap{}, fmt.Errorf("failed to append momentum clmm flash swap: builder=null")
	}
	if err := deployment.Validate(); err != nil {
		return FlashSwap{}, fmt.Errorf("failed to append momentum clmm flash swap: %w", err)
	}
	if pool.Address.IsZero() || pool.InitialVersion == 0 || amount == 0 || !validU128(sqrtPriceLimit) {
		return FlashSwap{}, fmt.Errorf("failed to append momentum clmm flash swap: parameters=invalid")
	}
	amountArg, err := builder.Pure(bcsUint64(amount))
	if err != nil {
		return FlashSwap{}, fmt.Errorf("failed to append momentum clmm flash swap: %w", err)
	}
	return appendFlashSwap(builder, deployment, pool, xForY, amountArg, sqrtPriceLimit)
}

func appendFlashSwap(builder *onchainSui.ProgrammableTransactionBuilder, deployment Deployment, pool Pool, xForY bool, amount onchainSui.Argument, sqrtPriceLimit *big.Int) (FlashSwap, error) {
	poolArg, err := builder.Object(onchainSui.InputKindShared, onchainSui.ObjectInput{Address: pool.Address, Version: pool.InitialVersion, Mutable: true})
	if err != nil {
		return FlashSwap{}, fmt.Errorf("failed to append momentum clmm flash swap: %w", err)
	}
	direction, _ := builder.Pure(bcsBool(xForY))
	exact, _ := builder.Pure(bcsBool(true))
	limit, _ := builder.Pure(bcsUint128(sqrtPriceLimit))
	clock := deployment.Clock
	clock.Mutable = false
	clockArg, err := builder.Object(onchainSui.InputKindShared, clock)
	if err != nil {
		return FlashSwap{}, fmt.Errorf("failed to append momentum clmm flash swap: %w", err)
	}
	version := deployment.Version
	version.Mutable = false
	versionArg, err := builder.Object(onchainSui.InputKindShared, version)
	if err != nil {
		return FlashSwap{}, fmt.Errorf("failed to append momentum clmm flash swap: %w", err)
	}
	result, err := builder.MoveCall(onchainSui.MoveCall{Package: deployment.PublishedAt, Module: deployment.TradeModule, Function: "flash_swap", TypeArguments: []string{pool.CoinTypeX, pool.CoinTypeY}, Arguments: []onchainSui.Argument{poolArg, direction, exact, amount, limit, clockArg, versionArg}})
	if err != nil {
		return FlashSwap{}, fmt.Errorf("failed to append momentum clmm flash swap: %w", err)
	}
	x, _ := onchainSui.NestedResult(result, 0)
	y, _ := onchainSui.NestedResult(result, 1)
	receipt, _ := onchainSui.NestedResult(result, 2)
	return FlashSwap{Pool: poolArg, Version: versionArg, BalanceX: x, BalanceY: y, Receipt: receipt}, nil
}

// AppendRepayFlashSwap appends Momentum flash-swap repayment.
//
// Parameters:
//   - builder: Programmable transaction builder.
//   - deployment: Momentum deployment.
//   - pool: Momentum pool.
//   - flashSwap: Original flash-swap arguments.
//   - balanceX: Repayment balance X.
//   - balanceY: Repayment balance Y.
//
// Returns:
//   - Validation error.
//
// Version:
//   - 2026-08-30: Added.
func AppendRepayFlashSwap(builder *onchainSui.ProgrammableTransactionBuilder, deployment Deployment, pool Pool, flashSwap FlashSwap, balanceX, balanceY onchainSui.Argument) error {
	if builder == nil {
		return fmt.Errorf("failed to append momentum clmm flash swap repayment: builder=null")
	}
	if err := deployment.Validate(); err != nil {
		return fmt.Errorf("failed to append momentum clmm flash swap repayment: %w", err)
	}
	_, err := builder.MoveCall(onchainSui.MoveCall{Package: deployment.PublishedAt, Module: deployment.TradeModule, Function: "repay_flash_swap", TypeArguments: []string{pool.CoinTypeX, pool.CoinTypeY}, Arguments: []onchainSui.Argument{flashSwap.Pool, flashSwap.Receipt, balanceX, balanceY, flashSwap.Version}})
	if err != nil {
		return fmt.Errorf("failed to append momentum clmm flash swap repayment: %w", err)
	}
	return nil
}

type Swap struct {
	Checkpoint      onchainSui.CheckpointSequenceNumber
	SequenceNumber  uint64
	Transaction     onchainSui.TransactionDigest
	EventIndex      uint32
	Timestamp       time.Time
	Sender          onchainSui.Address
	Pool            onchainSui.Address
	XForY           bool
	AmountX         uint64
	AmountY         uint64
	FeeAmount       uint64
	ProtocolFee     uint64
	SqrtPriceBefore string
	SqrtPriceAfter  string
}

// ParseSwapEvent parses a historical Momentum SwapEvent.
//
// Parameters:
//   - event: Sui event.
//
// Returns:
//   - Parsed swap.
//   - Parse error.
//
// Version:
//   - 2026-09-01: Accepted the deployed trade::SwapEvent type.
//   - 2026-08-30: Added.
func ParseSwapEvent(event onchainSui.Event) (Swap, error) {
	swap, err := parseSwapJSON(event.Type, event.JSON)
	if err != nil {
		return Swap{}, err
	}
	swap.SequenceNumber, swap.Transaction, swap.Timestamp = event.SequenceNumber, event.Transaction, event.Timestamp
	return swap, nil
}

// ParseLiveSwapEvent parses a live Momentum SwapEvent.
//
// Parameters:
//   - event: Sui live event.
//
// Returns:
//   - Parsed swap.
//   - Parse error.
//
// Version:
//   - 2026-08-30: Added.
func ParseLiveSwapEvent(event onchainSui.LiveEvent) (Swap, error) {
	swap, err := parseSwapJSON(event.Type, event.JSON)
	if err != nil {
		return Swap{}, err
	}
	swap.Checkpoint, swap.Transaction, swap.EventIndex = event.Checkpoint, event.Transaction, event.EventIndex
	return swap, nil
}

func parseSwapJSON(eventType string, data json.RawMessage) (Swap, error) {
	if !strings.HasSuffix(eventType, "::trade::SwapEvent") {
		return Swap{}, fmt.Errorf("failed to parse momentum clmm swap event: event_type=invalid")
	}
	var v struct {
		Sender   string          `json:"sender"`
		Pool     string          `json:"pool_id"`
		XForY    bool            `json:"x_for_y"`
		AmountX  json.RawMessage `json:"amount_x"`
		AmountY  json.RawMessage `json:"amount_y"`
		Before   json.RawMessage `json:"sqrt_price_before"`
		After    json.RawMessage `json:"sqrt_price_after"`
		Fee      json.RawMessage `json:"fee_amount"`
		Protocol json.RawMessage `json:"protocol_fee"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return Swap{}, fmt.Errorf("failed to parse momentum clmm swap event: failed to decode event: %w", err)
	}
	sender, err := onchainSui.ParseAddress(v.Sender)
	if err != nil {
		return Swap{}, fmt.Errorf("failed to parse momentum clmm swap event: sender=invalid: %w", err)
	}
	pool, err := onchainSui.ParseAddress(v.Pool)
	if err != nil {
		return Swap{}, fmt.Errorf("failed to parse momentum clmm swap event: pool=invalid: %w", err)
	}
	x, err := jsonUint64(v.AmountX)
	if err != nil {
		return Swap{}, fmt.Errorf("failed to parse momentum clmm swap event: amount_x=invalid")
	}
	y, err := jsonUint64(v.AmountY)
	if err != nil {
		return Swap{}, fmt.Errorf("failed to parse momentum clmm swap event: amount_y=invalid")
	}
	fee, err := jsonUint64(v.Fee)
	if err != nil {
		return Swap{}, fmt.Errorf("failed to parse momentum clmm swap event: fee=invalid")
	}
	protocol, err := jsonUint64(v.Protocol)
	if err != nil {
		return Swap{}, fmt.Errorf("failed to parse momentum clmm swap event: protocol_fee=invalid")
	}
	before, err := jsonUnsigned(v.Before)
	if err != nil {
		return Swap{}, fmt.Errorf("failed to parse momentum clmm swap event: before=invalid")
	}
	after, err := jsonUnsigned(v.After)
	if err != nil {
		return Swap{}, fmt.Errorf("failed to parse momentum clmm swap event: after=invalid")
	}
	return Swap{Sender: sender, Pool: pool, XForY: v.XForY, AmountX: x, AmountY: y, FeeAmount: fee, ProtocolFee: protocol, SqrtPriceBefore: before.String(), SqrtPriceAfter: after.String()}, nil
}

func moveTypeArguments(value string) ([]string, error) {
	start, end := strings.IndexByte(value, '<'), strings.LastIndexByte(value, '>')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("move_type=invalid")
	}
	body := value[start+1 : end]
	var out []string
	depth, offset := 0, 0
	for i, r := range body {
		if r == '<' {
			depth++
		}
		if r == '>' {
			depth--
		}
		if r == ',' && depth == 0 {
			out = append(out, strings.TrimSpace(body[offset:i]))
			offset = i + 1
		}
	}
	return append(out, strings.TrimSpace(body[offset:])), nil
}
func jsonUint64(value json.RawMessage) (uint64, error) {
	n, err := jsonUnsigned(value)
	if err != nil || !n.IsUint64() {
		return 0, fmt.Errorf("number=invalid")
	}
	return n.Uint64(), nil
}
func jsonUnsigned(value json.RawMessage) (*big.Int, error) {
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		var n json.Number
		if err := json.Unmarshal(value, &n); err != nil {
			return nil, err
		}
		s = n.String()
	}
	result, ok := new(big.Int).SetString(s, 10)
	if !ok || result.Sign() < 0 {
		return nil, fmt.Errorf("number=invalid")
	}
	return result, nil
}
func validU128(value *big.Int) bool { return value != nil && value.Sign() > 0 && value.BitLen() <= 128 }
func bcsBool(value bool) []byte {
	if value {
		return []byte{1}
	}
	return []byte{0}
}
func bcsUint64(value uint64) []byte {
	result := make([]byte, 8)
	binary.LittleEndian.PutUint64(result, value)
	return result
}
func bcsUint128(value *big.Int) []byte {
	result := make([]byte, 16)
	bytes := value.Bytes()
	for i := range bytes {
		result[i] = bytes[len(bytes)-1-i]
	}
	return result
}
