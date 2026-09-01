package clmm

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/big"

	onchainSui "github.com/k4k3ru-hub/onchain/go/sui"
)

type QuotePairParams struct {
	Bid QuoteExactInputParams
	Ask QuoteExactOutputParams
}

type QuotePairResult struct {
	Bid        QuoteResult
	Ask        QuoteResult
	Checkpoint onchainSui.CheckpointSequenceNumber
}

// QuotePair simulates bid and ask quotes in one programmable transaction.
//
// Parameters:
//   - ctx: Request context.
//   - params: Bid and ask quote parameters.
//
// Returns:
//   - Quote pair sharing one simulation checkpoint.
//   - Quote error.
//
// Version:
//   - 2026-09-01: Added.
func (q *Quoter) QuotePair(ctx context.Context, params QuotePairParams) (QuotePairResult, error) {
	if q == nil || q.simulator == nil {
		return QuotePairResult{}, fmt.Errorf("failed to quote momentum clmm pair: quoter=null")
	}
	if params.Bid.Sender != params.Ask.Sender || params.Bid.Pool.Address != params.Ask.Pool.Address || params.Bid.Pool.InitialVersion != params.Ask.Pool.InitialVersion {
		return QuotePairResult{}, fmt.Errorf("failed to quote momentum clmm pair: parameters=invalid")
	}
	builder := onchainSui.NewProgrammableTransactionBuilder()
	if err := q.appendPairQuote(builder, params.Bid.Pool, params.Bid.AmountIn, params.Bid.XForY, true, params.Bid.SqrtPriceLimit); err != nil {
		return QuotePairResult{}, fmt.Errorf("failed to quote momentum clmm pair: %w", err)
	}
	if err := q.appendPairQuote(builder, params.Ask.Pool, params.Ask.AmountOut, params.Ask.XForY, false, params.Ask.SqrtPriceLimit); err != nil {
		return QuotePairResult{}, fmt.Errorf("failed to quote momentum clmm pair: %w", err)
	}
	transaction, err := builder.Build()
	if err != nil {
		return QuotePairResult{}, fmt.Errorf("failed to quote momentum clmm pair: %w", err)
	}
	simulation, err := q.simulator.SimulateTransaction(ctx, onchainSui.SimulationRequest{Sender: params.Bid.Sender, Transaction: transaction})
	if err != nil {
		return QuotePairResult{}, fmt.Errorf("failed to quote momentum clmm pair: %w", err)
	}
	if simulation == nil || len(simulation.CommandResults) != 4 {
		return QuotePairResult{}, fmt.Errorf("failed to quote momentum clmm pair: command_result=invalid")
	}
	bidAmount, err := momentumPairAmount(simulation.CommandResults[1])
	if err != nil {
		return QuotePairResult{}, fmt.Errorf("failed to quote momentum clmm pair: bid=invalid: %w", err)
	}
	askAmount, err := momentumPairAmount(simulation.CommandResults[3])
	if err != nil {
		return QuotePairResult{}, fmt.Errorf("failed to quote momentum clmm pair: ask=invalid: %w", err)
	}
	checkpoint := simulation.Checkpoint
	return QuotePairResult{
		Bid:        QuoteResult{AmountIn: params.Bid.AmountIn, AmountOut: bidAmount, Checkpoint: checkpoint},
		Ask:        QuoteResult{AmountIn: askAmount, AmountOut: params.Ask.AmountOut, Checkpoint: checkpoint},
		Checkpoint: checkpoint,
	}, nil
}

func (q *Quoter) appendPairQuote(builder *onchainSui.ProgrammableTransactionBuilder, poolState Pool, amount uint64, xForY, exactInput bool, sqrtPriceLimit *big.Int) error {
	if builder == nil || amount == 0 || !validU128(sqrtPriceLimit) {
		return fmt.Errorf("failed to append momentum clmm pair quote: parameters=invalid")
	}
	pool, err := builder.Object(onchainSui.InputKindShared, onchainSui.ObjectInput{Address: poolState.Address, Version: poolState.InitialVersion})
	if err != nil {
		return fmt.Errorf("failed to append momentum clmm pair quote: %w", err)
	}
	direction, _ := builder.Pure(bcsBool(xForY))
	exact, _ := builder.Pure(bcsBool(exactInput))
	limit, _ := builder.Pure(bcsUint128(sqrtPriceLimit))
	amountArgument, _ := builder.Pure(bcsUint64(amount))
	state, err := builder.MoveCall(onchainSui.MoveCall{Package: q.deployment.PublishedAt, Module: q.deployment.TradeModule, Function: "compute_swap_result", TypeArguments: []string{poolState.CoinTypeX, poolState.CoinTypeY}, Arguments: []onchainSui.Argument{pool, direction, exact, limit, amountArgument}})
	if err != nil {
		return fmt.Errorf("failed to append momentum clmm pair quote: %w", err)
	}
	_, err = builder.MoveCall(onchainSui.MoveCall{Package: q.deployment.PublishedAt, Module: q.deployment.TradeModule, Function: "get_state_amount_calculated", Arguments: []onchainSui.Argument{state}})
	if err != nil {
		return fmt.Errorf("failed to append momentum clmm pair quote: %w", err)
	}
	return nil
}

func momentumPairAmount(command onchainSui.SimulationCommandResult) (uint64, error) {
	if len(command.ReturnValues) == 0 || len(command.ReturnValues[0].BCS) < 8 {
		return 0, fmt.Errorf("failed to parse momentum clmm pair amount: return_value=invalid")
	}
	amount := binary.LittleEndian.Uint64(command.ReturnValues[0].BCS[:8])
	if amount == 0 {
		return 0, fmt.Errorf("failed to parse momentum clmm pair amount: amount=empty")
	}
	return amount, nil
}

// QuotePair simulates bid and ask Momentum quotes in one programmable transaction.
//
// Parameters:
//   - ctx: Request context.
//   - params: Quote-pair parameters.
//
// Returns:
//   - Quote pair sharing one checkpoint.
//   - Quote error.
//
// Version:
//   - 2026-09-01: Added.
func (c *Client) QuotePair(ctx context.Context, params QuotePairParams) (QuotePairResult, error) {
	if c == nil || c.quoter == nil {
		return QuotePairResult{}, fmt.Errorf("failed to quote momentum clmm pair: client=null")
	}
	return c.quoter.QuotePair(ctx, params)
}
