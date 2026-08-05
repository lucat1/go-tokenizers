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

// TokenizersConfig is the configuration for the pool of Tokenizers.
// It configures how many tokenizer instances to use at a given time.
// Each tokenizer worker can process one encoding request at a time,
// thus parallelism is achieved via the worker pool.
type TokenizersConfig struct {
	// MaxWorkers is the maximum number of workers the pool can have running at
	// a given time when the system reaches saturation.
	MaxWorkers int
	// MinWorkers is the minimum number of workers the pool will have running at
	// any given point in time. When [Tokenizers] is first instantiated, MinWorkers
	// will be immediately started before [New] returns.
	// Must be <= [TokenizersConfig.MaxWorkers].
	MinWorkers int
	// IdleWorkers governs the number of workers that are kept alive as the
	// system gets to an idle state. When workers are returned to the pool, if
	// there is no more demand, they are closed until we reach [MinWorkers].
	//
	// Must be >= [TokenizersConfig.MinWorkers] and <= [TokenizersConfig.MaxWorkers].
	IdleWorkers int
}

// Tokenizers is the tokenizer pool used to concurrently compute string token
// encodings. Its parallelism is dictatd by the [TokenizersConfig] it's built
// with.
// Tokenizers is thread-safe and can be concurrently called by multiple goroutines.
// Internally, a single [wazero.Runtime] is shared across all parallel workers,
// where each worker maintains a [api.Module] instance with the tokenizer loaded.
type Tokenizers struct {
	rt     wazero.Runtime
	cm     wazero.CompiledModule
	closer api.Closer

	pool *pool[*tokenizer]
}

// New creates a new [Tokenizers] concurrent tokenizer pool. It can be used to
// compute tokenization of input texts. The provided [configuration] should be
// a JSON-encoded tokenizer as supported by the `huggingface/tokenizers` library.
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

// Close closes all the tokenizer workers and shuts down the WASM runtime.
func (t *Tokenizers) Close(ctx context.Context) error {
	return errors.Join(t.pool.close(ctx), t.closer.Close(ctx), t.rt.Close(ctx))
}

// Encode computes converst the input text into tokens. It's safe to call
// [Encode] concurrently. Parallelism is limited by the tokenizer worker pool
// configuration used to create [Tokenizers]. See [TokenizersConfig].
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
