package tokenizers

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

var (
	// ErrReadError indicates the failure to read the error message from the WASM runtime.
	ErrReadError = errors.New("could not read error message")
	// ErrInvalidReturns indicates an unexpected amount of return values from the WASM function call.
	ErrInvalidReturns = errors.New("expected exactly one return value")
	// ErrMemoryWrite indicates a failure to write to a WASM memory address.
	ErrMemoryWrite = errors.New("memory write failed")
	// ErrMemoryRead indicates a failure to read from a WASM memory address.
	ErrMemoryRead = errors.New("memory read failed")
	// ErrRuntime wraps any runtime error coming from the Rust code in the WASM
	// runtime. It can be used to differentiate between errors originating from
	// the Go wrapper or the Rust tokenizer implementation.
	ErrRuntime = errors.New("runtime error")
)

type tokenizer struct {
	mod api.Module
	mem api.Memory

	allocateFn      api.Function
	deallocateFn    api.Function
	loadTokenizerFn api.Function
	encodeFn        api.Function
}

func newTokenizer(ctx context.Context, rt wazero.Runtime, cm wazero.CompiledModule, configuration []byte) (*tokenizer, error) {
	mod, err := rt.InstantiateModule(ctx, cm, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("wazero module initialization: %w", err)
	}

	t := &tokenizer{
		mod: mod,
		mem: mod.Memory(),

		allocateFn:      mod.ExportedFunction("allocate"),
		deallocateFn:    mod.ExportedFunction("deallocate"),
		loadTokenizerFn: mod.ExportedFunction("load_tokenizer"),
		encodeFn:        mod.ExportedFunction("encode"),
	}

	if err := t.load(ctx, configuration); err != nil {
		rt.Close(ctx)
		return nil, err
	}

	return t, nil
}

func (t *tokenizer) close(ctx context.Context) error {
	return t.mod.Close(ctx)
}

func (t *tokenizer) load(ctx context.Context, bytes []byte) error {
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

	ok := t.mem.Write(uint32(ptr), bytes)
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

func firstReturn(returns []uint64) (uint64, error) {
	if len(returns) == 1 {
		return returns[0], nil
	}
	return 0, ErrInvalidReturns
}

func (t *tokenizer) alloc(ctx context.Context, size uint32) (uint32, error) {
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

func (t *tokenizer) dealloc(ctx context.Context, ptr uint32, size uint32) error {
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

func (t *tokenizer) decodeReturn(results []uint64) (vec, error) {
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

func (t *tokenizer) readError(ptr, size uint32) error {
	data, ok := t.mem.Read(ptr, size)
	if !ok {
		return ErrReadError
	}

	return fmt.Errorf("%w: %s", ErrRuntime, data)
}

func (t *tokenizer) encode(ctx context.Context, text string) (ids []uint32, err error) {
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

	if !t.mem.Write(uint32(ptr), bytes) {
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

	defer func() {
		if err := t.dealloc(ctx, vec.ptr, vec.size*4); err != nil {
			err = errors.Join(err, fmt.Errorf("deallocating output memory: %w", err))
		}
	}()

	idBytes, ok := t.mem.Read(vec.ptr, vec.size*4)
	if !ok {
		return nil, fmt.Errorf("read encode result: %w", ErrMemoryRead)
	}

	ids = make([]uint32, vec.size)
	for i := range vec.size {
		ids[i] = binary.LittleEndian.Uint32(idBytes[i*4 : i*4+4])
	}

	return ids, nil
}
