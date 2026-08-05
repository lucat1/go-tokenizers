package tokenizers

import (
	_ "embed"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTokenizers(t *testing.T) {
	ctx := t.Context()

	tok, err := New(ctx, configuration, TokenizersConfig{MaxWorkers: 1})
	assert.Nil(t, err)

	err = tok.Close(ctx)
	assert.Nil(t, err)
}

func TestTokenizersEncode(t *testing.T) {
	ctx := t.Context()

	tok, err := New(ctx, configuration, TokenizersConfig{MaxWorkers: 1})
	assert.Nil(t, err)

	ids, err := tok.Encode(ctx, "hello world, heLlo woRld")
	assert.Nil(t, err)
	assert.Equal(t, []uint32{1, 2, 0, 3, 4}, ids)

	err = tok.Close(ctx)
	assert.Nil(t, err)
}

func BenchmarkTokenizersEncode(b *testing.B) {
	ctx := b.Context()

	tok, err := New(ctx, configuration, TokenizersConfig{})
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
