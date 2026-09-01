package clmm

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"testing"

	onchainSui "github.com/k4k3ru-hub/onchain/go/sui"
)

type testSimulator struct {
	result  *onchainSui.SimulationResult
	request onchainSui.SimulationRequest
}

// SimulateTransaction returns the configured simulation result.
//
// Version:
//   - 2026-08-31: Added.
func (s *testSimulator) SimulateTransaction(_ context.Context, request onchainSui.SimulationRequest) (*onchainSui.SimulationResult, error) {
	s.request = request
	return s.result, nil
}

func TestPoolQuoteAndFlashSwap(t *testing.T) {
	poolAddress, _ := onchainSui.ParseAddress("0x9")
	object := &onchainSui.Object{Address: poolAddress, Version: 3, Move: &onchainSui.MoveObject{Type: "0x1::pool::Pool<0x2::sui::SUI, 0x3::usdc::USDC>", JSON: json.RawMessage(`{"reserve_x":"100","reserve_y":"200","sqrt_price":"10","liquidity":"300","tick_index":{"bits":"4294967295"},"swap_fee_rate":"100"}`)}}
	pool, err := ParsePool(object)
	if err != nil {
		t.Fatalf("ParsePool() returned an unexpected error: %v", err)
	}
	if pool.SqrtPrice.String() != "10" || pool.TickIndex != -1 || pool.FeeRate != 100 {
		t.Fatalf("ParsePool() = %+v", pool)
	}
	value := make([]byte, 8)
	binary.LittleEndian.PutUint64(value, 99)
	simulator := &testSimulator{result: &onchainSui.SimulationResult{CommandResults: []onchainSui.SimulationCommandResult{{}, {ReturnValues: []onchainSui.CommandOutput{{BCS: value}}}}}}
	quoter, err := NewQuoter(testDeployment(), simulator)
	if err != nil {
		t.Fatalf("NewQuoter() returned an unexpected error: %v", err)
	}
	sender, _ := onchainSui.ParseAddress("0xa")
	quote, err := quoter.QuoteExactInput(context.Background(), QuoteExactInputParams{Sender: sender, Pool: *pool, AmountIn: 100, XForY: true, SqrtPriceLimit: big.NewInt(5)})
	if err != nil || quote.AmountOut != 99 {
		t.Fatalf("QuoteExactInput()=%+v, %v", quote, err)
	}
	if got := simulator.request.Transaction.Inputs[2].Pure; len(got) != 1 || got[0] != 1 {
		t.Fatalf("QuoteExactInput() exact_input BCS = %v", got)
	}
	quote, err = quoter.QuoteExactOutput(context.Background(), QuoteExactOutputParams{Sender: sender, Pool: *pool, AmountOut: 99, XForY: true, SqrtPriceLimit: big.NewInt(5)})
	if err != nil || quote.AmountIn != 99 || quote.AmountOut != 99 {
		t.Fatalf("QuoteExactOutput()=%+v, %v", quote, err)
	}
	if got := simulator.request.Transaction.Inputs[2].Pure; len(got) != 1 || got[0] != 0 {
		t.Fatalf("QuoteExactOutput() exact_input BCS = %v", got)
	}
	builder := onchainSui.NewProgrammableTransactionBuilder()
	flash, err := AppendFlashSwap(builder, testDeployment(), *pool, true, 100, big.NewInt(5))
	if err != nil {
		t.Fatalf("AppendFlashSwap() returned an unexpected error: %v", err)
	}
	if err := AppendRepayFlashSwap(builder, testDeployment(), *pool, flash, flash.BalanceX, flash.BalanceY); err != nil {
		t.Fatalf("AppendRepayFlashSwap() returned an unexpected error: %v", err)
	}
	tx, err := builder.Build()
	if err != nil || len(tx.Commands) != 2 {
		t.Fatalf("Build()=%+v, %v", tx, err)
	}
}

func TestAppendAtomicSwap(t *testing.T) {
	poolAddress, _ := onchainSui.ParseAddress("0x9")
	builder := onchainSui.NewProgrammableTransactionBuilder()
	balanceX, _ := builder.MoveCall(onchainSui.MoveCall{Package: poolAddress, Module: "test", Function: "balance_x"})
	balanceY, _ := builder.MoveCall(onchainSui.MoveCall{Package: poolAddress, Module: "test", Function: "balance_y"})
	amount, _ := builder.MoveCall(onchainSui.MoveCall{Package: poolAddress, Module: "test", Function: "amount"})
	pool := Pool{Address: poolAddress, InitialVersion: 3, CoinTypeX: "0x2::sui::SUI", CoinTypeY: "0x3::usdc::USDC"}
	result, err := AppendAtomicSwap(builder, testDeployment(), AtomicSwapParams{Pool: pool, Balances: SwapBalances{BalanceX: balanceX, BalanceY: balanceY}, XForY: true, AmountIn: amount, SqrtPriceLimit: big.NewInt(5)})
	if err != nil {
		t.Fatalf("AppendAtomicSwap() returned an unexpected error: %v", err)
	}
	if result.BalanceX != balanceX || result.BalanceY != balanceY {
		t.Fatalf("AppendAtomicSwap() = %+v", result)
	}
	tx, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() returned an unexpected error: %v", err)
	}
	if len(tx.Commands) != 10 {
		t.Fatalf("len(Commands) = %d, want 10", len(tx.Commands))
	}
}

func testDeployment() Deployment {
	packageAddress, _ := onchainSui.ParseAddress("0x1")
	version, _ := onchainSui.ParseAddress("0x2")
	clock, _ := onchainSui.ParseAddress("0x6")
	return Deployment{Package: packageAddress, PublishedAt: packageAddress, Version: onchainSui.ObjectInput{Address: version, Version: 1}, Clock: onchainSui.ObjectInput{Address: clock, Version: 1}, TradeModule: "trade", EventsModule: "events"}
}
