package tokenizers

import (
	"context"
	_ "embed"
	"testing"

	"github.com/stretchr/testify/assert"
)

//go:embed tokenizer.json
var configuration []byte

func TestNew(t *testing.T) {
	ctx := context.Background()

	tok, err := New(ctx, configuration)
	assert.Nil(t, err)
	err = tok.Close(ctx)
	assert.Nil(t, err)
}

func TestEncode(t *testing.T) {
	ctx := context.Background()

	tok, err := New(ctx, configuration)
	assert.Nil(t, err)

	ids, err := tok.Encode(ctx, "hello world, heLlo woRld")
	assert.Nil(t, err)

	assert.Equal(t, []uint32{1, 2, 0, 3, 4}, ids)

	err = tok.Close(ctx)
	assert.Nil(t, err)
}

func BenchmarkEncode(b *testing.B) {
	ctx := context.Background()

	tok, err := New(ctx, configuration)
	assert.Nil(b, err)

	text := "hello world this is a benchmark"

	b.ResetTimer()

	for b.Loop() {
		_, err := tok.Encode(ctx, text)
		assert.Nil(b, err)
	}

	err = tok.Close(ctx)
	assert.Nil(b, err)
}
