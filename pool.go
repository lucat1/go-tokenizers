package tokenizers

import (
	"context"
	"errors"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

var (
	ErrInvalidPoolConfiguration = errors.New("invalid pool configuration")
	ErrInvalidWorkers           = errors.New("minWorkers > maxWorkers")
	ErrClosed                   = errors.New("tokenizer pool is shutting down")
	ErrLeak                     = errors.New("pool shutdown leaked tokenizers")
)

type pool struct {
	ctx context.Context

	idle  chan *tokenizer
	newFn func() (*tokenizer, error)

	created    atomic.Int32
	maxWorkers int
	closed     atomic.Bool
}

func newPool(
	ctx context.Context,
	minWorkers, maxWorkers, maxIdle int,
	newFn func(ctx context.Context) (*tokenizer, error),
) (*pool, error) {
	if minWorkers < 0 || maxWorkers <= 0 || maxIdle < 0 {
		return nil, ErrInvalidPoolConfiguration
	}
	if minWorkers > maxWorkers {
		return nil, ErrInvalidWorkers
	}
	if maxIdle > maxWorkers {
		maxIdle = maxWorkers
	}

	p := &pool{
		idle:       make(chan *tokenizer, maxIdle),
		maxWorkers: maxWorkers,
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(int(maxWorkers))
	tokenizers := make([]*tokenizer, minWorkers)
	for i := range minWorkers {
		eg.Go(func() error {
			tok, err := newFn(ctx)
			if err != nil {
				return err
			}

			tokenizers[i] = tok
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	for _, tok := range tokenizers {
		p.idle <- tok
	}

	return p, nil
}

func (p *pool) get(ctx context.Context) (*tokenizer, error) {
	if p.closed.Load() {
		return nil, ErrClosed
	}

	// If we have a tokenizers to use right await, quickly return.
	select {
	case tok := <-p.idle:
		return tok, nil
	default:
	}

	for {
		n := p.created.Load()
		if int(n) >= p.maxWorkers {
			// Cannot create a new tokenizer, the pool is already saturated.
			// Exit from loop and wait on p.idle
			break
		}
		if p.created.CompareAndSwap(n, n+1) {
			tok, err := p.newFn()
			if err != nil {
				p.created.Add(-1)
				return nil, err
			}
			return tok, nil
		}
	}

	// Otherwise wait.
	select {
	case tok := <-p.idle:
		return tok, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *pool) put(tok *tokenizer) error {
	if !p.closed.Load() && len(p.idle) < cap(p.idle) {
		p.idle <- tok
		return nil
	}

	// Too many idle tokenizers; shrink.
	p.created.Add(-1)
	return tok.close(p.ctx)
}

func (p *pool) close(ctx context.Context) error {
	p.closed.Store(true)
	for len(p.idle) > 0 {
		select {
		case tok := <-p.idle:
			tok.close(ctx)
			p.created.Add(-1)
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if p.created.Load() > 0 {
		return ErrLeak
	}
	return nil
}
