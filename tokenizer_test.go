package tokenizers

import (
	"context"
	_ "embed"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed tokenizer.json
var configuration []byte

func newTestTokenizer(t assert.TestingT, ctx context.Context, configuration []byte) (*tokenizer, error) {
	rt := wazero.NewRuntime(ctx)
	_, err := wasi_snapshot_preview1.Instantiate(ctx, rt)
	assert.NotNil(t, err)

	cm, err := rt.CompileModule(ctx, wasmBytes)
	assert.NotNil(t, err)
	return newTokenizer(ctx, rt, cm, configuration)
}

func TestNew(t *testing.T) {
	ctx := context.Background()

	tok, err := newTestTokenizer(t, ctx, configuration)
	assert.Nil(t, err)

	err = tok.close(ctx)
	assert.Nil(t, err)
}

func TestEncode(t *testing.T) {
	ctx := context.Background()

	tok, err := newTestTokenizer(t, ctx, configuration)
	assert.Nil(t, err)

	ids, err := tok.encode(ctx, "hello world, heLlo woRld")
	assert.Nil(t, err)

	assert.Equal(t, []uint32{1, 2, 0, 3, 4}, ids)

	err = tok.close(ctx)
	assert.Nil(t, err)
}

func BenchmarkEncode(b *testing.B) {
	ctx := context.Background()

	tok, err := newTestTokenizer(b, ctx, configuration)
	assert.Nil(b, err)

	text := "hello world this is a benchmark"

	b.ResetTimer()

	for b.Loop() {
		_, err := tok.encode(ctx, text)
		assert.Nil(b, err)
	}

	err = tok.close(ctx)
	assert.Nil(b, err)
}
