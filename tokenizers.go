package tokenizers

import (
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

var (
	ErrReadError      = errors.New("could not read error message")
	ErrInvalidReturns = errors.New("expected exactly one return value")
	ErrMemoryWrite    = errors.New("memory write failed")
	ErrMemoryRead     = errors.New("memory read failed")
	ErrRuntime        = errors.New("runtime error")
)

//go:embed tokenizers.wasm
var wasmBytes []byte

type Tokenizer struct {
	runtime wazero.Runtime
	module  api.Module
	closer  api.Closer

	allocateFn      api.Function
	deallocateFn    api.Function
	loadTokenizerFn api.Function
	encodeFn        api.Function
}

func New(ctx context.Context, configuration []byte) (*Tokenizer, error) {
	rt := wazero.NewRuntime(ctx)

	closer, err := wasi_snapshot_preview1.Instantiate(ctx, rt)
	if err != nil {
		return nil, fmt.Errorf("could not instantiate wasi_snapshot_preview1 runtime: %w", err)
	}

	mod, err := rt.Instantiate(ctx, wasmBytes)
	if err != nil {
		return nil, err
	}

	t := &Tokenizer{
		runtime: rt,
		module:  mod,
		closer:  closer,

		allocateFn:      mod.ExportedFunction("allocate"),
		deallocateFn:    mod.ExportedFunction("deallocate"),
		loadTokenizerFn: mod.ExportedFunction("load_tokenizer"),
		encodeFn:        mod.ExportedFunction("encode"),
	}

	if err := t.loadTokenizer(ctx, configuration); err != nil {
		rt.Close(ctx)
		return nil, err
	}

	return t, nil
}

func (t *Tokenizer) Close(ctx context.Context) error {
	if err := t.closer.Close(ctx); err != nil {
		return err
	}
	return t.runtime.Close(ctx)
}

func firstReturn(returns []uint64) (uint64, error) {
	if len(returns) == 1 {
		return returns[0], nil
	}
	return 0, ErrInvalidReturns
}

func (t *Tokenizer) loadTokenizer(ctx context.Context, bytes []byte) error {
	size := uint32(len(bytes))
	ptr, err := t.alloc(ctx, size)
	if err != nil {
		return err
	}
	defer func() {
		if e := t.dealloc(ctx, ptr, size); e != nil {
			err = errors.Join(err, e)
		}
	}()

	ok := t.module.Memory().Write(uint32(ptr), bytes)
	if !ok {
		return fmt.Errorf("write input tokenizer: %w", ErrMemoryWrite)
	}

	results, err := t.loadTokenizerFn.Call(ctx, uint64(ptr), uint64(size))
	if err != nil {
		return fmt.Errorf("load_tokenizer: %w", err)
	}

	_, err = t.decodeReturn(results)
	return err
}

func (t *Tokenizer) Encode(ctx context.Context, text string) (ids []uint32, err error) {
	bytes := []byte(text)
	size := uint32(len(bytes))

	ptr, err := t.alloc(ctx, size)
	if err != nil {
		return nil, err
	}
	defer func() {
		if e := t.dealloc(ctx, ptr, size); e != nil {
			err = errors.Join(err, fmt.Errorf("deallocating input memory: %w", err))
		}
	}()

	if !t.module.Memory().Write(uint32(ptr), bytes) {
		return nil, fmt.Errorf("write input text: %w", ErrMemoryWrite)
	}

	results, err := t.encodeFn.Call(ctx, uint64(ptr), uint64(size))
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	vec, err := t.decodeReturn(results)
	if err != nil {
		return nil, err
	}

	if vec.size == 0 {
		return []uint32{}, nil
	}

	idBytes, ok := t.module.Memory().Read(vec.ptr, vec.size*4)
	if !ok {
		return nil, fmt.Errorf("read encode result: %w", ErrMemoryRead)
	}

	ids = make([]uint32, vec.size)
	for i := range vec.size {
		ids[i] = binary.LittleEndian.Uint32(idBytes[i*4 : i*4+4])
	}

	defer func() {
		if err := t.dealloc(ctx, vec.ptr, vec.size*4); err != nil {
			err = errors.Join(err, fmt.Errorf("deallocating output memory: %w", err))
		}
	}()

	return ids, nil
}

func (t *Tokenizer) alloc(ctx context.Context, size uint32) (uint32, error) {
	res, err := t.allocateFn.Call(ctx, uint64(size))
	if err != nil {
		return 0, fmt.Errorf("allocate: %W", err)
	}
	ptr, err := firstReturn(res)
	if err != nil {
		return 0, err
	}
	return uint32(ptr), nil
}

func (t *Tokenizer) dealloc(ctx context.Context, ptr uint32, size uint32) error {
	_, err := t.deallocateFn.Call(ctx, uint64(ptr), uint64(size))
	if err != nil {
		return fmt.Errorf("deallocate: %w", err)
	}

	return nil
}

type vec struct {
	ptr  uint32
	size uint32
}

func (t *Tokenizer) decodeReturn(results []uint64) (vec, error) {
	data, err := firstReturn(results)
	if err != nil {
		return vec{}, err
	}

	isErr := (data >> 63) != 0
	ptr := uint32(data >> 31)
	size := uint32(data & 0x7fffffff)

	if isErr {
		return vec{}, t.readError(ptr, size)
	}
	return vec{ptr: ptr, size: size}, nil
}

func (t *Tokenizer) readError(ptr, size uint32) error {
	data, ok := t.module.Memory().Read(ptr, size)
	if !ok {
		return ErrReadError
	}

	return fmt.Errorf("%w: %s", ErrRuntime, data)
}
