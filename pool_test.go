package tokenizers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockTokenizer struct {
	counter *int
}

// close implements closer.
func (mt *mockTokenizer) close(ctx context.Context) error {
	*mt.counter++
	return nil
}

func newMockTokenizerFactory(open, close *int) func(ctx context.Context) (*mockTokenizer, error) {
	return func(ctx context.Context) (*mockTokenizer, error) {
		*open++
		return &mockTokenizer{close}, nil
	}
}

func TestNewPool(t *testing.T) {
	ctx := t.Context()

	pool, err := newPool(ctx, 0, 1, 1, newMockTokenizerFactory(new(0), new(0)))
	assert.Nil(t, err)

	err = pool.close(ctx)
	assert.Nil(t, err)
}

func TestPoolGet(t *testing.T) {
	ctx := t.Context()

	open := 0
	close := 0

	pool, err := newPool(ctx, 0, 1, 1, newMockTokenizerFactory(&open, &close))
	assert.Nil(t, err)

	tok, err := pool.get(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, tok)
	assert.Equal(t, 1, open)
	err = pool.put(tok)
	assert.Nil(t, err)

	err = pool.close(ctx)
	assert.Equal(t, 1, close)
	assert.Nil(t, err)
}

func TestPoolTwoGet(t *testing.T) {
	ctx := t.Context()

	open := 0
	close := 0

	pool, err := newPool(ctx, 1, 2, 1, newMockTokenizerFactory(&open, &close))
	assert.Nil(t, err)
	assert.Equal(t, 1, open)

	tok1, err := pool.get(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, tok1)

	tok2, err := pool.get(ctx)
	assert.Nil(t, err)
	assert.NotNil(t, tok1)

	err = pool.put(tok1)
	assert.Nil(t, err)
	assert.Equal(t, 0, close)

	err = pool.put(tok2)
	assert.Nil(t, err)
	assert.Equal(t, 1, close)

	err = pool.close(ctx)
	assert.Equal(t, 2, close)
	assert.Nil(t, err)
}
