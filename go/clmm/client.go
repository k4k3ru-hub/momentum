package clmm

import (
	"context"
	"fmt"

	onchainSui "github.com/k4k3ru-hub/onchain/go/sui"
)

type Client struct {
	deployment Deployment
	rpc        *onchainSui.RPCClient
	grpc       *onchainSui.GRPCClient
	quoter     *Quoter
}
type SwapQuery struct {
	Pool             *onchainSui.Address
	AfterCheckpoint  *onchainSui.CheckpointSequenceNumber
	AtCheckpoint     *onchainSui.CheckpointSequenceNumber
	BeforeCheckpoint *onchainSui.CheckpointSequenceNumber
	First            int
	After            string
}
type SwapPage struct {
	Swaps       []Swap
	HasNextPage bool
	NextCursor  string
}
type SwapNotification struct {
	Swap       *Swap
	Checkpoint *onchainSui.CheckpointSequenceNumber
}
type SwapSubscription struct {
	subscription *onchainSui.EventSubscription
	pool         *onchainSui.Address
}

// NewClient composes a Momentum CLMM client.
//
// Parameters:
//   - deployment: Momentum deployment.
//   - rpcClient: Sui GraphQL client.
//   - grpcClient: Sui gRPC client.
//
// Returns:
//   - Composed client.
//   - Construction error.
//
// Version:
//   - 2026-08-31: Added.
func NewClient(deployment Deployment, rpcClient *onchainSui.RPCClient, grpcClient *onchainSui.GRPCClient) (*Client, error) {
	if rpcClient == nil {
		return nil, fmt.Errorf("failed to create momentum clmm client: rpc_client=null")
	}
	if grpcClient == nil {
		return nil, fmt.Errorf("failed to create momentum clmm client: grpc_client=null")
	}
	quoter, err := NewQuoter(deployment, grpcClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create momentum clmm client: %w", err)
	}
	return &Client{deployment: deployment, rpc: rpcClient, grpc: grpcClient, quoter: quoter}, nil
}

// Pool gets and parses a Momentum CLMM pool.
//
// Parameters:
//   - ctx: Request context.
//   - address: Pool address.
//
// Returns:
//   - Pool state.
//   - Retrieval error.
//
// Version:
//   - 2026-08-31: Added.
func (c *Client) Pool(ctx context.Context, address onchainSui.Address) (Pool, error) {
	if c == nil || c.rpc == nil {
		return Pool{}, fmt.Errorf("failed to get momentum clmm pool: client=null")
	}
	object, err := c.rpc.Object(ctx, address)
	if err != nil {
		return Pool{}, fmt.Errorf("failed to get momentum clmm pool: %w", err)
	}
	pool, err := ParsePool(object)
	if err != nil {
		return Pool{}, fmt.Errorf("failed to get momentum clmm pool: %w", err)
	}
	return *pool, nil
}

// QuoteExactInput simulates a Momentum exact-input swap.
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
//   - 2026-08-31: Added.
func (c *Client) QuoteExactInput(ctx context.Context, params QuoteExactInputParams) (QuoteResult, error) {
	if c == nil || c.quoter == nil {
		return QuoteResult{}, fmt.Errorf("failed to quote momentum clmm exact input: client=null")
	}
	return c.quoter.QuoteExactInput(ctx, params)
}

// QuoteExactOutput simulates a Momentum exact-output swap.
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
//   - 2026-09-01: Added.
func (c *Client) QuoteExactOutput(ctx context.Context, params QuoteExactOutputParams) (QuoteResult, error) {
	if c == nil || c.quoter == nil {
		return QuoteResult{}, fmt.Errorf("failed to quote momentum clmm exact output: client=null")
	}
	return c.quoter.QuoteExactOutput(ctx, params)
}

// Swaps gets historical Momentum swaps.
//
// Parameters:
//   - ctx: Request context.
//   - query: Swap query.
//
// Returns:
//   - Swap page.
//   - Retrieval error.
//
// Version:
//   - 2026-08-31: Added.
func (c *Client) Swaps(ctx context.Context, query SwapQuery) (SwapPage, error) {
	if c == nil || c.rpc == nil {
		return SwapPage{}, fmt.Errorf("failed to get momentum clmm swaps: client=null")
	}
	if query.Pool != nil && query.Pool.IsZero() {
		return SwapPage{}, fmt.Errorf("failed to get momentum clmm swaps: pool=empty")
	}
	page, err := c.rpc.Events(ctx, onchainSui.EventQuery{Filter: onchainSui.EventFilter{Type: c.swapEventType(), AfterCheckpoint: query.AfterCheckpoint, AtCheckpoint: query.AtCheckpoint, BeforeCheckpoint: query.BeforeCheckpoint}, First: query.First, After: query.After})
	if err != nil {
		return SwapPage{}, fmt.Errorf("failed to get momentum clmm swaps: %w", err)
	}
	swaps := make([]Swap, 0, len(page.Events))
	for _, event := range page.Events {
		swap, err := ParseSwapEvent(event)
		if err != nil {
			return SwapPage{}, fmt.Errorf("failed to get momentum clmm swaps: %w", err)
		}
		if query.Pool == nil || swap.Pool == *query.Pool {
			swaps = append(swaps, swap)
		}
	}
	return SwapPage{Swaps: swaps, HasNextPage: page.HasNextPage, NextCursor: page.NextCursor}, nil
}

// SubscribeSwaps subscribes to live Momentum swaps.
//
// Parameters:
//   - ctx: Subscription context.
//   - pool: Optional pool filter.
//
// Returns:
//   - Swap subscription.
//   - Subscription error.
//
// Version:
//   - 2026-08-31: Added.
func (c *Client) SubscribeSwaps(ctx context.Context, pool *onchainSui.Address) (*SwapSubscription, error) {
	if c == nil || c.grpc == nil {
		return nil, fmt.Errorf("failed to subscribe momentum clmm swaps: client=null")
	}
	if pool != nil && pool.IsZero() {
		return nil, fmt.Errorf("failed to subscribe momentum clmm swaps: pool=empty")
	}
	subscription, err := c.grpc.SubscribeEvents(ctx, onchainSui.LiveEventFilter{Type: c.swapEventType()})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe momentum clmm swaps: %w", err)
	}
	return &SwapSubscription{subscription: subscription, pool: pool}, nil
}

// Recv waits for the next matching Momentum swap or progress notification.
//
// Parameters:
//   - ctx: Receive context.
//
// Returns:
//   - Swap notification.
//   - Receive error.
//
// Version:
//   - 2026-08-31: Added.
func (s *SwapSubscription) Recv(ctx context.Context) (*SwapNotification, error) {
	if s == nil || s.subscription == nil {
		return nil, fmt.Errorf("failed to receive momentum clmm swap: subscription=null")
	}
	for {
		notification, err := s.subscription.Recv(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to receive momentum clmm swap: %w", err)
		}
		if notification.Event == nil {
			return &SwapNotification{Checkpoint: notification.Watermark.Checkpoint}, nil
		}
		swap, err := ParseLiveSwapEvent(*notification.Event)
		if err != nil {
			return nil, fmt.Errorf("failed to receive momentum clmm swap: %w", err)
		}
		if s.pool == nil || swap.Pool == *s.pool {
			return &SwapNotification{Swap: &swap, Checkpoint: notification.Watermark.Checkpoint}, nil
		}
	}
}

// Close closes the Momentum swap subscription.
//
// Version:
//   - 2026-08-31: Added.
func (s *SwapSubscription) Close() {
	if s != nil && s.subscription != nil {
		s.subscription.Close()
	}
}
func (c *Client) swapEventType() string {
	return c.deployment.Package.String() + "::" + c.deployment.EventsModule + "::SwapEvent"
}
