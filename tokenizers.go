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

	pool *pool[*tokenizer]
}

func New(ctx context.Context, configuration []byte, config TokenizersConfig) (*Tokenizers, error) {
	if config.MaxWorkers == 0 {
		config.MaxWorkers = runtime.NumCPU()
	}
	if config.MinWorkers == 0 {
		config.MinWorkers = min(max(1, config.MaxWorkers/4), config.MaxWorkers)
	}
	if config.IdleWorkers == 0 {
		config.IdleWorkers = config.MinWorkers
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

func (t *Tokenizers) Encode(ctx context.Context, text string) (tokens []uint32, err error) {
	tok, err := t.pool.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting tokenizer from pool: %w", err)
	}
	defer func() {
		if e := t.pool.put(tok); e != nil {
			err = errors.Join(err, e)
		}
	}()

	return tok.encode(ctx, text)
}
