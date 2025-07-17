package dbwriter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWriterImplementsDBWriter は Writer が DBWriter インターフェースを実装していることを確認します。
func TestWriterImplementsDBWriter(t *testing.T) {
	assert.Implements(t, (*DBWriter)(nil), new(Writer))
}
