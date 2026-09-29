package testsupport

import (
	"bytes"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AssertEventSchema validates an encoded event (envelope + data) against
// api/events/<schemaFile>, the WS0' event contract.
func AssertEventSchema(t testing.TB, schemaFile string, payload []byte) {
	t.Helper()
	_, self, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(self), "..", "..", "..", "..", "api", "events")
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	sch, err := c.Compile(filepath.Join(dir, schemaFile))
	require.NoError(t, err)
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(payload))
	require.NoError(t, err)
	assert.NoError(t, sch.Validate(inst), string(payload))
}
