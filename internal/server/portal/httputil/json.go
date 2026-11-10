package httputil

import (
	"bytes"
	"sync"

	json "design-cloud-engine/internal/infra/jsoncodec"

	"github.com/labstack/echo/v5"
)

const maxPooledJSONBuf = 1 << 16 // 64 KiB

var jsonBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// JSONCodecSerializer adapts project-wide jsoncodec (encoding/json/v2) to Echo's JSONSerializer.
// It uses portal JSON semantics: deterministic keys, nil slices as [], and nil maps as {}.
type JSONCodecSerializer struct{}

// Serialize encodes target with jsoncodec.MarshalPortal and writes it to the response.
func (JSONCodecSerializer) Serialize(c *echo.Context, target any, indent string) error {
	var (
		b   []byte
		err error
	)
	if indent != "" {
		b, err = json.MarshalPortalIndent(target, "", indent)
	} else {
		b, err = json.MarshalPortal(target)
	}
	if err != nil {
		return err
	}
	_, err = c.Response().Write(b)
	return err
}

// Deserialize reads the request body into a pooled buffer and decodes JSON via jsoncodec.
func (JSONCodecSerializer) Deserialize(c *echo.Context, target any) error {
	buf := jsonBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= maxPooledJSONBuf {
			jsonBufPool.Put(buf)
		}
	}()

	if _, err := buf.ReadFrom(c.Request().Body); err != nil {
		if echo.StatusCode(err) != 0 {
			return err
		}
		return echo.ErrBadRequest.Wrap(err)
	}
	if err := json.Unmarshal(buf.Bytes(), target); err != nil {
		return echo.ErrBadRequest.Wrap(err)
	}
	return nil
}
