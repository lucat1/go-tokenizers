package tokenizers

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

const graceTime = time.Second * 5

var (
	// ErrInvalidPoolConfiguration is returned if any of the pool worker
	// configuration values don't make sense on their own.
	ErrInvalidPoolConfiguration = errors.New("invalid pool configuration")
	// ErrInvalidWorkers is returned if any of the pool worker configuration
	// values are incompatible among themselves.
	ErrInvalidWorkers = errors.New("minWorkers > maxWorkers or minWorkers > maxIdle")
	// ErrClosed is returned when attempting to obtain a worker from a pool which
	// has been closed.
	ErrClosed = errors.New("tokenizer pool is shutting down")
	// ErrLeak is returned when the pool leaked workers: some workers were taken
	// from the pool but not returned, so we can't guarantee they have been closed.
	ErrLeak = errors.New("pool shutdown leaked tokenizers")
)

type closer interface {
	close(ctx context.Context) error
}

type pool[T closer] struct {
	ctx context.Context

	idle  chan T
	unit  T
	newFn func(ctx context.Context) (T, error)

	created    atomic.Int32
	maxWorkers int
	closed     atomic.Bool
}

func newPool[T closer](
	ctx context.Context,
	minWorkers, maxWorkers, maxIdle int,
	newFn func(ctx context.Context) (T, error),
) (*pool[T], error) {
	if minWorkers < 0 || maxWorkers <= 0 || maxIdle < 0 {
		return nil, ErrInvalidPoolConfiguration
	}
	if minWorkers > maxWorkers || minWorkers > maxIdle {
		return nil, ErrInvalidWorkers
	}
	if maxIdle > maxWorkers {
		maxIdle = maxWorkers
	}

	p := &pool[T]{
		idle:  make(chan T, maxIdle),
		newFn: newFn,

		maxWorkers: maxWorkers,
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(int(maxWorkers))
	workers := make([]T, minWorkers)
	for i := range minWorkers {
		eg.Go(func() error {
			w, err := newFn(ctx)
			if err != nil {
				return err
			}

			workers[i] = w
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	for _, tok := range workers {
		p.idle <- tok
	}

	return p, nil
}

func (p *pool[T]) get(ctx context.Context) (T, error) {
	if p.closed.Load() {
		return p.unit, ErrClosed
	}

	// If we have a worker to use right await, quickly return.
	select {
	case tok := <-p.idle:
		return tok, nil
	default:
	}

	for {
		n := p.created.Load()
		if int(n) >= p.maxWorkers {
			// Cannot create a new worker, the pool is already saturated.
			// Exit from loop and wait on p.idle
			break
		}
		if p.created.CompareAndSwap(n, n+1) {
			tok, err := p.newFn(p.ctx)
			if err != nil {
				p.created.Add(-1)
				return p.unit, err
			}
			return tok, nil
		}
	}

	// Otherwise wait.
	select {
	case tok := <-p.idle:
		return tok, nil
	case <-ctx.Done():
		return p.unit, ctx.Err()
	}
}

func (p *pool[T]) put(w T) error {
	if !p.closed.Load() && len(p.idle) < cap(p.idle) {
		p.idle <- w
		return nil
	}

	// Too many idle workers: shrink.
	p.created.Add(-1)
	return w.close(p.ctx)
}

func (p *pool[T]) close(ctx context.Context) error {
	var closeErrors []error

	p.closed.Store(true)
	for len(p.idle) > 0 {
		select {
		case tok := <-p.idle:
			if err := tok.close(ctx); err != nil {
				closeErrors = append(closeErrors, err)
			}
			p.created.Add(-1)
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	graceCtx, cancel := context.WithTimeout(ctx, graceTime)
	defer cancel()

	for p.created.Load() > 0 && graceCtx.Err() == nil {
		select {
		case tok := <-p.idle:
			if err := tok.close(ctx); err != nil {
				closeErrors = append(closeErrors, err)
			}
			p.created.Add(-1)
		case <-graceCtx.Done():
			// If our grace context triggered the timeout, then we are accepting that
			// the pool will leak workers, and we should return this as an error.
			if graceCtx.Err() != nil {
				closeErrors = append(closeErrors, ErrLeak)
				break
			}

			closeErrors = append(closeErrors, ctx.Err())
		}
	}

	return errors.Join(closeErrors...)
}
