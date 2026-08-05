package tokenizers

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"runtime"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed tokenizers.wasm
var wasmBytes []byte

type TokenizersConfig struct {
	MaxWorkers  int
	MinWorkers  int
	IdleWorkers int
}

type Tokenizers struct {
	rt     wazero.Runtime
	cm     wazero.CompiledModule
	closer api.Closer

	pool *pool
}

func New(ctx context.Context, configuration []byte, config TokenizersConfig) (*Tokenizers, error) {
	if config.MaxWorkers == 0 {
		config.MaxWorkers = runtime.NumCPU()
	}
	if config.MinWorkers == 0 {
		config.MinWorkers = max(1, config.MaxWorkers/4)
	}

	rt := wazero.NewRuntime(ctx)

	closer, err := wasi_snapshot_preview1.Instantiate(ctx, rt)
	if err != nil {
		return nil, fmt.Errorf("instantiating wasi_snapshot_preview1: %w", err)
	}

	cm, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("compiling WASM module: %w", err)
	}

	newFn := func(ctx context.Context) (*tokenizer, error) {
		return newTokenizer(ctx, rt, cm, configuration)
	}
	pool, err := newPool(ctx, config.MinWorkers, config.MaxWorkers, config.IdleWorkers, newFn)
	if err != nil {
		return nil, fmt.Errorf("creating worker pool: %w", err)
	}

	toks := Tokenizers{
		rt:     rt,
		cm:     cm,
		closer: closer,

		pool: pool,
	}

	return &toks, nil
}

func (t *Tokenizers) Close(ctx context.Context) error {
	return errors.Join(t.pool.close(ctx), t.closer.Close(ctx), t.rt.Close(ctx))
}

func (t *Tokenizers) Encode(ctx context.Context, text string) ([]uint32, error) {
	tok, err := t.pool.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting tokenizer from pool: %w", err)
	}

	return tok.encode(ctx, text)
}
